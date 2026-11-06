// Package arca is Tramo's own SOAP client for ARCA web services: WSAA
// (access tickets), ws_sr_constancia_inscripcion (getPersona_v2) and WSCDC
// (ComprobanteConstatar). No third party SDK and no intermediary: the
// certificate and its key never leave the arca-gateway process.
//
// Message shapes, namespaces and endpoints come from the manuals in
// eng/arca/manuales (WSAA spec, constancia v4.1, WSCDC). See README.md.
package arca

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const soapEnvNS = "http://schemas.xmlsoap.org/soap/envelope/"

// maxResponse bounds what we read from ARCA.
const maxResponse = 4 << 20

// FaultError is a SOAP fault returned by a service.
type FaultError struct {
	Code   string // faultcode, e.g. "ns1:coe.alreadyAuthenticated"
	String string // faultstring
}

func (f *FaultError) Error() string { return "arca: soap fault " + f.Code + ": " + f.String }

// Is lets callers match a fault by the code after its namespace prefix:
// errors.Is(err, &FaultError{Code: "coe.alreadyAuthenticated"}).
func (f *FaultError) Is(target error) bool {
	t, ok := target.(*FaultError)
	if !ok {
		return false
	}
	return localCode(f.Code) == localCode(t.Code)
}

func localCode(c string) string {
	if i := strings.LastIndex(c, ":"); i >= 0 {
		return c[i+1:]
	}
	return c
}

// ErrUnavailable wraps transport failures and HTTP errors without a fault.
var ErrUnavailable = errors.New("arca: service unavailable")

type soapClient struct {
	http *http.Client
}

func newSOAPClient(timeout time.Duration) soapClient {
	return soapClient{http: &http.Client{Timeout: timeout}}
}

type envelopeOut struct {
	XMLName xml.Name `xml:"soapenv:Envelope"`
	NS      string   `xml:"xmlns:soapenv,attr"`
	Header  struct{} `xml:"soapenv:Header"`
	Body    struct {
		Inner any
	} `xml:"soapenv:Body"`
}

type envelopeIn struct {
	Body struct {
		Fault *struct {
			Code   string `xml:"faultcode"`
			String string `xml:"faultstring"`
		} `xml:"Fault"`
		Inner []byte `xml:",innerxml"`
	} `xml:"Body"`
}

// call posts body (a struct marshalled with its own namespace) and decodes
// the first element of the response body into out.
func (c soapClient) call(ctx context.Context, url, action string, body, out any) error {
	env := envelopeOut{NS: soapEnvNS}
	env.Body.Inner = body
	payload, err := xml.Marshal(env)
	if err != nil {
		return fmt.Errorf("arca: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(append([]byte(xml.Header), payload...)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `"`+action+`"`)
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("%w: read: %v", ErrUnavailable, err)
	}
	var in envelopeIn
	if err := xml.Unmarshal(raw, &in); err != nil {
		if res.StatusCode >= 400 {
			return fmt.Errorf("%w: http %d", ErrUnavailable, res.StatusCode)
		}
		return fmt.Errorf("arca: decode envelope: %w", err)
	}
	if f := in.Body.Fault; f != nil {
		return &FaultError{Code: strings.TrimSpace(f.Code), String: strings.TrimSpace(f.String)}
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("%w: http %d", ErrUnavailable, res.StatusCode)
	}
	if err := xml.Unmarshal(in.Body.Inner, out); err != nil {
		return fmt.Errorf("arca: decode body: %w", err)
	}
	return nil
}
