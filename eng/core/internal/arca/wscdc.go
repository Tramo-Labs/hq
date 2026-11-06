package arca

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// ServiceWSCDC is the WSAA service id of the voucher check.
const ServiceWSCDC = "wscdc"

// wscdcNS is the namespace in the WSCDC manual examples. The SOAPAction is
// the ASP.NET convention (namespace + method); the manual does not print it.
const (
	wscdcNS     = "http://servicios1.afip.gob.ar/wscdc/"
	wscdcAction = wscdcNS + "ComprobanteConstatar"
)

// Comprobante identifies a voucher to check.
type Comprobante struct {
	Modo            string // CAE, CAEA or CAI
	CUITEmisor      int64
	PuntoVenta      int32
	Tipo            int32
	Numero          int64
	Fecha           string // YYYYMMDD
	ImporteTotal    money.Amount
	CodAutorizacion string
	DocTipoReceptor int32 // 0 = not sent
	DocNroReceptor  string
}

// Obs is an observation, error or event code with its message.
type Obs struct {
	Code int32  `xml:"Code"`
	Msg  string `xml:"Msg"`
}

// Constatacion is the answer of ComprobanteConstatar.
type Constatacion struct {
	Aprobado      bool // Resultado A
	Observaciones []Obs
	Errores       []Obs
	FechaProceso  string
}

// WSCDC calls ComprobanteConstatar with Tramo's own CUIT.
type WSCDC struct {
	soap    soapClient
	url     string
	tickets *WSAA
	cuit    int64
}

// NewWSCDC builds the client.
func NewWSCDC(url string, tickets *WSAA, ownCUIT int64, s soapClient) *WSCDC {
	return &WSCDC{soap: s, url: url, tickets: tickets, cuit: ownCUIT}
}

type cmpReq struct {
	CbteModo        string `xml:"CbteModo"`
	CuitEmisor      int64  `xml:"CuitEmisor"`
	PtoVta          int32  `xml:"PtoVta"`
	CbteTipo        int32  `xml:"CbteTipo"`
	CbteNro         int64  `xml:"CbteNro"`
	CbteFch         string `xml:"CbteFch"`
	ImpTotal        string `xml:"ImpTotal"`
	CodAutorizacion string `xml:"CodAutorizacion"`
	DocTipoReceptor string `xml:"DocTipoReceptor,omitempty"`
	DocNroReceptor  string `xml:"DocNroReceptor,omitempty"`
}

// Constatar checks c against ARCA.
func (w *WSCDC) Constatar(ctx context.Context, c Comprobante) (Constatacion, error) {
	ta, err := w.tickets.Ticket(ctx, ServiceWSCDC)
	if err != nil {
		return Constatacion{}, err
	}
	req := struct {
		XMLName xml.Name `xml:"ComprobanteConstatar"`
		NS      string   `xml:"xmlns,attr"`
		Auth    struct {
			Token string `xml:"Token"`
			Sign  string `xml:"Sign"`
			Cuit  int64  `xml:"Cuit"`
		} `xml:"Auth"`
		CmpReq cmpReq `xml:"CmpReq"`
	}{NS: wscdcNS}
	req.Auth.Token, req.Auth.Sign, req.Auth.Cuit = ta.Token, ta.Sign, w.cuit
	req.CmpReq = cmpReq{
		CbteModo: c.Modo, CuitEmisor: c.CUITEmisor, PtoVta: c.PuntoVenta, CbteTipo: c.Tipo,
		CbteNro: c.Numero, CbteFch: c.Fecha, ImpTotal: c.ImporteTotal.String(), CodAutorizacion: c.CodAutorizacion,
		DocNroReceptor: c.DocNroReceptor,
	}
	if c.DocTipoReceptor != 0 {
		req.CmpReq.DocTipoReceptor = fmt.Sprint(c.DocTipoReceptor)
	}
	var res struct {
		Result struct {
			Resultado     string `xml:"Resultado"`
			FchProceso    string `xml:"FchProceso"`
			Observaciones []Obs  `xml:"Observaciones>Obs"`
			Errors        []Obs  `xml:"Errors>Err"`
		} `xml:"ComprobanteConstatarResult"`
	}
	if err := w.soap.call(ctx, w.url, wscdcAction, req, &res); err != nil {
		return Constatacion{}, err
	}
	r := res.Result
	out := Constatacion{Observaciones: r.Observaciones, Errores: r.Errors, FechaProceso: r.FchProceso}
	switch strings.TrimSpace(r.Resultado) {
	case "A":
		out.Aprobado = true
	case "R":
	default:
		// No verdict: infrastructure or authentication errors only.
		return out, fmt.Errorf("%w: %s", ErrNoVerdict, describe(r.Errors))
	}
	return out, nil
}

// ErrNoVerdict means WSCDC answered without Resultado (errors only).
var ErrNoVerdict = errors.New("arca: WSCDC answered without a verdict")

func describe(obs []Obs) string {
	parts := make([]string, 0, len(obs))
	for _, o := range obs {
		parts = append(parts, fmt.Sprintf("%d %s", o.Code, o.Msg))
	}
	return strings.Join(parts, "; ")
}
