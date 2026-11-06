package arca

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

// ServiceConstancia is the WSAA service id of the registration record.
const ServiceConstancia = "ws_sr_constancia_inscripcion"

const constanciaNS = "http://a5.soap.ws.server.puc.sr/"

// ErrPersonaNotFound is ARCA's "No existe persona con ese Id".
var ErrPersonaNotFound = errors.New("arca: no person with that CUIT")

// Impuesto is one tax registration as ARCA reports it.
type Impuesto struct {
	ID          int32  `xml:"idImpuesto"`
	Descripcion string `xml:"descripcionImpuesto"`
	Estado      string `xml:"estadoImpuesto"`
	Periodo     string `xml:"periodo"`
}

// Persona is what the gateway keeps from getPersona_v2.
type Persona struct {
	CUIT                 int64
	TipoPersona          string
	Nombre               string
	EstadoClave          string
	CondicionIVA         CondicionIVA
	Impuestos            []Impuesto
	CategoriaMonotributo string
	Domicilio            Domicilio
	Avisos               []string
	Consultado           string
}

// Domicilio is the fiscal address.
type Domicilio struct {
	Direccion string `xml:"direccion"`
	Localidad string `xml:"localidad"`
	CodPostal string `xml:"codPostal"`
	Provincia string `xml:"descripcionProvincia"`
}

// CondicionIVA is derived only from active taxes ARCA reports; anything
// else is left undetermined for a human or for rules in core.
type CondicionIVA int

const (
	IVANoDeterminada CondicionIVA = iota
	IVAResponsableInscripto
	IVAMonotributo
)

// Tax ids used to derive CondicionIVA (seen in the constancia v4.1 examples:
// 30 IVA, 20 MONOTRIBUTO).
const (
	impuestoIVA         = 30
	impuestoMonotributo = 20
)

type personaReturn struct {
	Metadata struct {
		FechaHora string `xml:"fechaHora"`
	} `xml:"metadata"`
	DatosGenerales *struct {
		IDPersona   int64     `xml:"idPersona"`
		TipoPersona string    `xml:"tipoPersona"`
		EstadoClave string    `xml:"estadoClave"`
		Nombre      string    `xml:"nombre"`
		Apellido    string    `xml:"apellido"`
		RazonSocial string    `xml:"razonSocial"`
		Domicilio   Domicilio `xml:"domicilioFiscal"`
	} `xml:"datosGenerales"`
	DatosRegimenGeneral *struct {
		Impuestos []Impuesto `xml:"impuesto"`
	} `xml:"datosRegimenGeneral"`
	DatosMonotributo *struct {
		Impuestos []Impuesto `xml:"impuesto"`
		Categoria struct {
			Descripcion string `xml:"descripcionCategoria"`
		} `xml:"categoriaMonotributo"`
	} `xml:"datosMonotributo"`
	ErrorConstancia *struct {
		Errors []string `xml:"error"`
	} `xml:"errorConstancia"`
	ErrorRegimenGeneral *struct {
		Errors  []string `xml:"error"`
		Mensaje string   `xml:"mensaje"`
	} `xml:"errorRegimenGeneral"`
	ErrorMonotributo *struct {
		Errors  []string `xml:"error"`
		Mensaje string   `xml:"mensaje"`
	} `xml:"errorMonotributo"`
}

// Constancia queries ws_sr_constancia_inscripcion with Tramo's own CUIT.
type Constancia struct {
	soap    soapClient
	url     string
	tickets *WSAA
	cuit    int64 // cuitRepresentada: Tramo's own CUIT, present in the TA relations
}

// NewConstancia builds the client.
func NewConstancia(url string, tickets *WSAA, ownCUIT int64, s soapClient) *Constancia {
	return &Constancia{soap: s, url: url, tickets: tickets, cuit: ownCUIT}
}

// GetPersona calls getPersona_v2 for cuit.
func (c *Constancia) GetPersona(ctx context.Context, cuit int64) (Persona, error) {
	ta, err := c.tickets.Ticket(ctx, ServiceConstancia)
	if err != nil {
		return Persona{}, err
	}
	req := struct {
		XMLName          xml.Name `xml:"a5:getPersona_v2"`
		NS               string   `xml:"xmlns:a5,attr"`
		Token            string   `xml:"token"`
		Sign             string   `xml:"sign"`
		CuitRepresentada int64    `xml:"cuitRepresentada"`
		IDPersona        int64    `xml:"idPersona"`
	}{NS: constanciaNS, Token: ta.Token, Sign: ta.Sign, CuitRepresentada: c.cuit, IDPersona: cuit}
	var res struct {
		Return personaReturn `xml:"personaReturn"`
	}
	if err := c.soap.call(ctx, c.url, "", req, &res); err != nil {
		return Persona{}, err
	}
	return mapPersona(cuit, res.Return)
}

func mapPersona(cuit int64, r personaReturn) (Persona, error) {
	if r.DatosGenerales == nil {
		if r.ErrorConstancia != nil {
			return Persona{}, fmt.Errorf("%w: %s", ErrPersonaNotFound, strings.Join(r.ErrorConstancia.Errors, "; "))
		}
		return Persona{}, errors.New("arca: getPersona_v2 answered without datosGenerales")
	}
	g := r.DatosGenerales
	p := Persona{
		CUIT: cuit, TipoPersona: g.TipoPersona, EstadoClave: g.EstadoClave,
		Domicilio: g.Domicilio, Consultado: r.Metadata.FechaHora,
	}
	p.Nombre = strings.TrimSpace(g.RazonSocial)
	if p.Nombre == "" {
		p.Nombre = strings.TrimSpace(g.Apellido + " " + g.Nombre)
	}
	if r.DatosRegimenGeneral != nil {
		p.Impuestos = append(p.Impuestos, r.DatosRegimenGeneral.Impuestos...)
	}
	if r.DatosMonotributo != nil {
		p.Impuestos = append(p.Impuestos, r.DatosMonotributo.Impuestos...)
		p.CategoriaMonotributo = r.DatosMonotributo.Categoria.Descripcion
	}
	for _, e := range []*struct {
		Errors  []string `xml:"error"`
		Mensaje string   `xml:"mensaje"`
	}{r.ErrorRegimenGeneral, r.ErrorMonotributo} {
		if e != nil {
			p.Avisos = append(p.Avisos, e.Errors...)
		}
	}
	if r.ErrorConstancia != nil {
		p.Avisos = append(p.Avisos, r.ErrorConstancia.Errors...)
	}
	p.CondicionIVA = condicionIVA(p.Impuestos)
	return p, nil
}

func condicionIVA(imps []Impuesto) CondicionIVA {
	active := func(id int32) bool {
		for _, i := range imps {
			if i.ID == id && strings.EqualFold(strings.TrimSpace(i.Estado), "AC") {
				return true
			}
		}
		return false
	}
	switch {
	case active(impuestoMonotributo):
		return IVAMonotributo
	case active(impuestoIVA):
		return IVAResponsableInscripto
	}
	return IVANoDeterminada
}
