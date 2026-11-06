// Package sim is a local stand-in for ARCA (WSAA, ws_sr_constancia_inscripcion
// and WSCDC) used with ARCA_ENV=sim and in CI. It speaks the same SOAP
// messages as the manuals in eng/arca/manuales and behaves like WSAA where
// it matters for the gateway: it verifies the CMS signature against a trust
// pool, issues 12 hour tickets, and refuses a second ticket for the same
// service while one is valid (coe.alreadyAuthenticated).
//
// What is NOT ARCA's: the fixture people and vouchers (fictitious CUITs),
// the fault codes for a bad token on the business services, and the
// observation codes of WSCDC rejections. Never draw conclusions about real
// ARCA error codes from this package.
package sim

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Paths, the same as the real endpoints so only the host changes.
const (
	PathWSAA       = "/ws/services/LoginCms"
	PathConstancia = "/sr-padron/webservices/personaServiceA5"
	PathWSCDC      = "/WSCDC/service.asmx"
)

// TicketTTL is how long a simulated TA lasts (WSAA: 12 hours).
const TicketTTL = 12 * time.Hour

// Impuesto is a tax line of a fixture person.
type Impuesto struct {
	ID          int32
	Descripcion string
	Estado      string
	Periodo     string
}

// Persona is a fixture registration record.
type Persona struct {
	CUIT                                       int64
	TipoPersona, EstadoClave                   string
	Nombre, Apellido, RazonSocial              string
	Direccion, Localidad, CodPostal, Provincia string
	RegimenGeneral                             []Impuesto
	Monotributo                                []Impuesto
	CategoriaMonotributo                       string
}

// Voucher is a fixture authorized voucher.
type Voucher struct {
	Modo            string
	CUITEmisor      int64
	PtoVta, Tipo    int32
	Numero          int64
	Fecha           string
	ImpTotal        money.Amount
	CodAutorizacion string
}

type ticket struct {
	service, sign string
	cuit          int64
	expires       time.Time
}

// Sim is the simulator. Create it with New; it is safe for concurrent use.
type Sim struct {
	trust    *x509.CertPool
	Now      func() time.Time
	Personas map[int64]Persona
	Vouchers []Voucher

	mu      sync.Mutex
	tickets map[string]ticket    // token -> ticket
	active  map[string]time.Time // service|cert serial -> expiry
	logins  map[string]int       // service -> loginCms calls received
}

// New returns a simulator that trusts certificates issued by trust, with
// the default fixtures.
func New(trust *x509.CertPool) *Sim {
	return &Sim{
		trust: trust, Now: time.Now, Personas: DefaultPersonas(), Vouchers: DefaultVouchers(),
		tickets: map[string]ticket{}, active: map[string]time.Time{}, logins: map[string]int{},
	}
}

// Logins reports how many loginCms requests the simulator got for service.
func (s *Sim) Logins(service string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins[service]
}

// Handler serves the three services.
func (s *Sim) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+PathWSAA, s.loginCms)
	mux.HandleFunc("POST "+PathConstancia, s.getPersona)
	mux.HandleFunc("POST "+PathWSCDC, s.constatar)
	return mux
}

// --- WSAA ---

func (s *Sim) loginCms(w http.ResponseWriter, r *http.Request) {
	var req struct {
		In0 string `xml:"in0"`
	}
	if !decode(w, r, &req) {
		return
	}
	der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(req.In0), ""))
	if err != nil {
		fault(w, "ns1:cms.bad.base64", "No se puede decodificar el BASE64")
		return
	}
	p7, err := pkcs7.Parse(der)
	if err != nil {
		fault(w, "ns1:cms.bad", "El CMS no es valido")
		return
	}
	if err := p7.VerifyWithChain(s.trust); err != nil {
		fault(w, "ns1:cms.sign.invalid", "Firma inválida o algoritmo no soportado")
		return
	}
	signer := p7.GetOnlySigner()
	if signer == nil {
		fault(w, "ns1:cms.cert.notFound", "No se ha encontrado certificado de firma en el CMS")
		return
	}
	var tra struct {
		Header struct {
			GenerationTime string `xml:"generationTime"`
			ExpirationTime string `xml:"expirationTime"`
		} `xml:"header"`
		Service string `xml:"service"`
	}
	if err := xml.Unmarshal(p7.Content, &tra); err != nil {
		fault(w, "ns1:xml.bad", "No se ha podido interpretar el XML contra el SCHEMA")
		return
	}
	now := s.Now()
	gen, err1 := time.Parse(time.RFC3339, tra.Header.GenerationTime)
	exp, err2 := time.Parse(time.RFC3339, tra.Header.ExpirationTime)
	if err1 != nil || err2 != nil || gen.After(now) || now.Sub(gen) > 24*time.Hour || !exp.After(now) || exp.Sub(now) > 24*time.Hour {
		fault(w, "ns1:xml.generationTime.invalid", "generationTime o expirationTime fuera de tolerancia")
		return
	}
	switch tra.Service {
	case "ws_sr_constancia_inscripcion", "wscdc":
	default:
		fault(w, "ns1:coe.notAuthorized", "Computador no autorizado a acceder al servicio")
		return
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(signer.Subject.SerialNumber, "CUIT "), 10, 64)
	if err != nil {
		fault(w, "ns1:cms.cert.invalid", "serialNumber del certificado sin CUIT")
		return
	}

	s.mu.Lock()
	s.logins[tra.Service]++
	key := tra.Service + "|" + signer.SerialNumber.String()
	if until, ok := s.active[key]; ok && now.Before(until) {
		s.mu.Unlock()
		fault(w, "ns1:coe.alreadyAuthenticated", "El CEE ya posee un TA valido para el acceso al WSN solicitado")
		return
	}
	t := ticket{service: tra.Service, sign: random(), cuit: n, expires: now.Add(TicketTTL)}
	token := random()
	s.tickets[token] = t
	s.active[key] = t.expires
	s.mu.Unlock()

	// source is WSAA's own DN, CUIT 33693450239: ARCA's CUIT, not a
	// taxpayer's. Cited from eng/arca/manuales/wsaaspec.txt:219 (production
	// DN) and wsaaspec.txt:220 (homologación DN).
	ltr := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<loginTicketResponse version="1.0"><header><source>CN=wsaahomo, O=AFIP, C=AR, SERIALNUMBER=CUIT 33693450239</source><destination>%s</destination><uniqueId>%d</uniqueId><generationTime>%s</generationTime><expirationTime>%s</expirationTime></header><credentials><token>%s</token><sign>%s</sign></credentials></loginTicketResponse>`,
		xmlText(signer.Subject.String()), now.Unix()&0xffffffff, now.Format(time.RFC3339), t.expires.Format(time.RFC3339), token, t.sign)
	respond(w, struct {
		XMLName xml.Name `xml:"loginCmsResponse"`
		NS      string   `xml:"xmlns,attr"`
		Return  string   `xml:"loginCmsReturn"`
	}{NS: "http://wsaa.view.sua.dvadac.desein.afip.gov", Return: ltr})
}

// valid checks a token for service and returns the CUIT it represents.
func (s *Sim) valid(token, sign, service string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[token]
	if !ok || t.sign != sign || t.service != service || !s.Now().Before(t.expires) {
		return 0, false
	}
	return t.cuit, true
}

// --- ws_sr_constancia_inscripcion ---

type impuestoXML struct {
	ID          int32  `xml:"idImpuesto"`
	Descripcion string `xml:"descripcionImpuesto"`
	Estado      string `xml:"estadoImpuesto"`
	Periodo     string `xml:"periodo"`
}

func (s *Sim) getPersona(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token            string `xml:"token"`
		Sign             string `xml:"sign"`
		CuitRepresentada int64  `xml:"cuitRepresentada"`
		IDPersona        int64  `xml:"idPersona"`
	}
	if !decode(w, r, &req) {
		return
	}
	cuit, ok := s.valid(req.Token, req.Sign, "ws_sr_constancia_inscripcion")
	if !ok {
		fault(w, "soap:Server", "sim: token o firma invalidos o vencidos")
		return
	}
	if cuit != req.CuitRepresentada {
		fault(w, "soap:Server", "sim: cuitRepresentada no incluida en el token")
		return
	}
	type general struct {
		IDPersona   int64  `xml:"idPersona"`
		TipoPersona string `xml:"tipoPersona"`
		TipoClave   string `xml:"tipoClave"`
		EstadoClave string `xml:"estadoClave"`
		Nombre      string `xml:"nombre,omitempty"`
		Apellido    string `xml:"apellido,omitempty"`
		RazonSocial string `xml:"razonSocial,omitempty"`
		Domicilio   struct {
			Direccion string `xml:"direccion"`
			Localidad string `xml:"localidad"`
			CodPostal string `xml:"codPostal"`
			Provincia string `xml:"descripcionProvincia"`
		} `xml:"domicilioFiscal"`
	}
	type ret struct {
		Metadata struct {
			FechaHora string `xml:"fechaHora"`
			Servidor  string `xml:"servidor"`
		} `xml:"metadata"`
		DatosGenerales *general `xml:"datosGenerales,omitempty"`
		Regimen        *struct {
			Impuestos []impuestoXML `xml:"impuesto"`
		} `xml:"datosRegimenGeneral,omitempty"`
		Monotributo *struct {
			Impuestos []impuestoXML `xml:"impuesto"`
			Categoria struct {
				Descripcion string `xml:"descripcionCategoria"`
			} `xml:"categoriaMonotributo"`
		} `xml:"datosMonotributo,omitempty"`
		ErrorConstancia *struct {
			Error     string `xml:"error"`
			IDPersona int64  `xml:"idPersona"`
		} `xml:"errorConstancia,omitempty"`
	}
	out := ret{}
	out.Metadata.FechaHora = s.Now().Format("2006-01-02T15:04:05.000-07:00")
	out.Metadata.Servidor = "tramo-arca-sim"
	p, found := s.Personas[req.IDPersona]
	if !found {
		out.ErrorConstancia = &struct {
			Error     string `xml:"error"`
			IDPersona int64  `xml:"idPersona"`
		}{Error: "No existe persona con ese Id", IDPersona: req.IDPersona}
	} else {
		g := &general{IDPersona: p.CUIT, TipoPersona: p.TipoPersona, TipoClave: "CUIT", EstadoClave: p.EstadoClave,
			Nombre: p.Nombre, Apellido: p.Apellido, RazonSocial: p.RazonSocial}
		g.Domicilio.Direccion, g.Domicilio.Localidad, g.Domicilio.CodPostal, g.Domicilio.Provincia = p.Direccion, p.Localidad, p.CodPostal, p.Provincia
		out.DatosGenerales = g
		if len(p.RegimenGeneral) > 0 {
			out.Regimen = &struct {
				Impuestos []impuestoXML `xml:"impuesto"`
			}{Impuestos: toXML(p.RegimenGeneral)}
		}
		if len(p.Monotributo) > 0 {
			m := &struct {
				Impuestos []impuestoXML `xml:"impuesto"`
				Categoria struct {
					Descripcion string `xml:"descripcionCategoria"`
				} `xml:"categoriaMonotributo"`
			}{Impuestos: toXML(p.Monotributo)}
			m.Categoria.Descripcion = p.CategoriaMonotributo
			out.Monotributo = m
		}
	}
	respond(w, struct {
		XMLName xml.Name `xml:"ns2:getPersona_v2Response"`
		NS      string   `xml:"xmlns:ns2,attr"`
		Return  ret      `xml:"personaReturn"`
	}{NS: "http://a5.soap.ws.server.puc.sr/", Return: out})
}

func toXML(in []Impuesto) []impuestoXML {
	out := make([]impuestoXML, len(in))
	for i, x := range in {
		out[i] = impuestoXML(x)
	}
	return out
}

// --- WSCDC ---

type obs struct {
	Code int32  `xml:"Code"`
	Msg  string `xml:"Msg"`
}

func (s *Sim) constatar(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Auth struct {
			Token string `xml:"Token"`
			Sign  string `xml:"Sign"`
			Cuit  int64  `xml:"Cuit"`
		} `xml:"Auth"`
		CmpReq struct {
			CbteModo        string `xml:"CbteModo"`
			CuitEmisor      int64  `xml:"CuitEmisor"`
			PtoVta          int32  `xml:"PtoVta"`
			CbteTipo        int32  `xml:"CbteTipo"`
			CbteNro         int64  `xml:"CbteNro"`
			CbteFch         string `xml:"CbteFch"`
			ImpTotal        string `xml:"ImpTotal"`
			CodAutorizacion string `xml:"CodAutorizacion"`
		} `xml:"CmpReq"`
	}
	if !decode(w, r, &req) {
		return
	}
	type result struct {
		Resultado     string `xml:"Resultado,omitempty"`
		Observaciones []obs  `xml:"Observaciones>Obs,omitempty"`
		FchProceso    string `xml:"FchProceso"`
		Errors        []obs  `xml:"Errors>Err,omitempty"`
	}
	res := result{FchProceso: s.Now().Format("20060102150405")}
	cuit, ok := s.valid(req.Auth.Token, req.Auth.Sign, "wscdc")
	c := req.CmpReq
	switch {
	case !ok || cuit != req.Auth.Cuit:
		res.Errors = []obs{{Code: 9001, Msg: "sim: token, firma o CUIT de Auth invalidos"}}
	default:
		res.Resultado = "R"
		amount, err := money.Parse(c.ImpTotal, money.DecimalDot)
		var v *Voucher
		for i := range s.Vouchers {
			x := &s.Vouchers[i]
			if x.CUITEmisor == c.CuitEmisor && x.PtoVta == c.PtoVta && x.Tipo == c.CbteTipo && x.Numero == c.CbteNro {
				v = x
			}
		}
		switch {
		case err != nil:
			res.Errors = []obs{{Code: 9002, Msg: "sim: ImpTotal con formato invalido"}}
		case v == nil:
			res.Observaciones = []obs{{Code: 9101, Msg: "sim: comprobante no registrado"}}
		default:
			for _, m := range []struct {
				ok   bool
				code int32
				msg  string
			}{
				{v.Modo == c.CbteModo, 9102, "sim: modalidad distinta"},
				{v.Fecha == c.CbteFch, 9103, "sim: fecha distinta"},
				{v.ImpTotal == amount, 9104, "sim: importe distinto"},
				{v.CodAutorizacion == c.CodAutorizacion, 9105, "sim: codigo de autorizacion distinto"},
			} {
				if !m.ok {
					res.Observaciones = append(res.Observaciones, obs{Code: m.code, Msg: m.msg})
				}
			}
			if len(res.Observaciones) == 0 {
				res.Resultado = "A"
			}
		}
	}
	respond(w, struct {
		XMLName xml.Name `xml:"ComprobanteConstatarResponse"`
		NS      string   `xml:"xmlns,attr"`
		Result  result   `xml:"ComprobanteConstatarResult"`
	}{NS: "http://servicios1.afip.gob.ar/wscdc/", Result: res})
}

// --- SOAP plumbing ---

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fault(w, "soap:Client", "sim: cannot read request")
		return false
	}
	var env struct {
		Body struct {
			Inner []byte `xml:",innerxml"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(raw, &env); err != nil || xml.Unmarshal(env.Body.Inner, out) != nil {
		fault(w, "soap:Client", "sim: malformed SOAP request")
		return false
	}
	return true
}

func respond(w http.ResponseWriter, body any) {
	b, err := xml.Marshal(body)
	if err != nil {
		http.Error(w, "sim: marshal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>%s</soap:Body></soap:Envelope>`, b)
}

func fault(w http.ResponseWriter, code, msg string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><soapenv:Fault><faultcode>%s</faultcode><faultstring>%s</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`,
		xmlText(code), xmlText(msg))
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func random() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}
