package arca

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// The parsers must read what ARCA documents, not only what our simulator
// writes. These responses are copied verbatim from the manuals (testdata).
//
// The only non synthetic CUITs in the repo are here, taken verbatim from
// ARCA's own manual examples:
//   - 20164755100: eng/arca/manuales/wsci41.txt lines 337 and 393
//     (getPersona_v2 example request and response, constancia v4.1 3.2.3).
//   - 20000000001: eng/arca/manuales/wscdc.txt lines 776 and 801
//     (ComprobanteConstatar example, WSCDC 2.2.6).
// Tramo's own CUIT in these tests is the synthetic 30000000023.

func serveFile(t *testing.T, path string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stubTickets returns a WSAA whose cache already holds valid tickets, so the
// business clients can be pointed at a canned response.
func stubTickets() *WSAA {
	w := NewWSAA("http://unused", Credentials{}, time.Second)
	for _, s := range []string{ServiceConstancia, ServiceWSCDC} {
		w.entry(s).ticket = Ticket{Token: "t", Sign: "s", Expires: time.Now().Add(time.Hour)}
	}
	return w
}

// Without credentials the client fails with an error, never a panic: a
// broken ticket cache in these tests used to crash the whole package.
func TestMissingCredentialsIsAnError(t *testing.T) {
	if _, err := SignTRA([]byte("<x/>"), Credentials{}); err == nil {
		t.Fatal("signed with no certificate")
	}
	empty := NewWSAA("http://127.0.0.1:1", Credentials{}, time.Second)
	c := NewConstancia("http://127.0.0.1:1", empty, 30000000023, newSOAPClient(time.Second))
	if _, err := c.GetPersona(context.Background(), 20164755100); err == nil {
		t.Fatal("GetPersona without ticket nor credentials returned no error")
	}
}

func TestParsesManualGetPersonaV2Response(t *testing.T) {
	srv := serveFile(t, "testdata/getPersona_v2_manual.xml")
	c := NewConstancia(srv.URL, stubTickets(), 30000000023, newSOAPClient(5*time.Second))
	p, err := c.GetPersona(context.Background(), 20164755100)
	if err != nil {
		t.Fatal(err)
	}
	if p.TipoPersona != "FISICA" || p.EstadoClave != "ACTIVO" || p.Nombre != "SKIOGEK OGIUPE OGIEK KPSGR" {
		t.Fatalf("datos generales: %+v", p)
	}
	if p.Domicilio.Localidad != "SAN MANUEL" || p.Domicilio.CodPostal != "7007" || p.Domicilio.Provincia != "BUENOS AIRES" {
		t.Fatalf("domicilio: %+v", p.Domicilio)
	}
	// Three <impuesto> in datosRegimenGeneral (11, 30, 308); the idImpuesto
	// inside categoriaAutonomo is not a tax line.
	if len(p.Impuestos) != 3 || p.CondicionIVA != IVAResponsableInscripto {
		t.Fatalf("impuestos %d, condicion %v: %+v", len(p.Impuestos), p.CondicionIVA, p.Impuestos)
	}
	if p.Consultado != "2026-02-26T15:52:37.653-03:00" {
		t.Fatalf("metadata: %q", p.Consultado)
	}
}

func TestParsesManualComprobanteConstatarResponse(t *testing.T) {
	srv := serveFile(t, "testdata/comprobanteConstatar_manual.xml")
	c := NewWSCDC(srv.URL, stubTickets(), 30000000023, newSOAPClient(5*time.Second))
	r, err := c.Constatar(context.Background(), Comprobante{Modo: "CAE", CUITEmisor: 20000000001, PuntoVenta: 1, Tipo: 1, Numero: 2,
		Fecha: "20101014", ImporteTotal: 30080, CodAutorizacion: "60428000005029"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Aprobado || r.FechaProceso != "20130729204436" || len(r.Errores) != 0 {
		t.Fatalf("constatacion: %+v", r)
	}
}

func TestSOAPFaultIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><soapenv:Fault>` +
			`<faultcode xmlns:ns1="http://xml.apache.org/axis/">ns1:coe.alreadyAuthenticated</faultcode>` +
			`<faultstring>El CEE ya posee un TA valido para el acceso al WSN solicitado</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`))
	}))
	defer srv.Close()
	var out struct{}
	err := newSOAPClient(time.Second).call(context.Background(), srv.URL, "", struct{}{}, &out)
	if err == nil || !errors.Is(err, FaultAlreadyAuthenticated) {
		t.Fatalf("got %v", err)
	}
}
