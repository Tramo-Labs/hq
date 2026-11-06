package arca_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/Tramo-Labs/hq/eng/core/internal/arca"
	"github.com/Tramo-Labs/hq/eng/core/internal/arca/sim"
)

const tramoCUIT = 30000000023

type env struct {
	sim   *sim.Sim
	srv   *httptest.Server
	creds arca.Credentials
}

func newEnv(t *testing.T) env {
	t.Helper()
	trust, creds, err := sim.NewCredentials(tramoCUIT)
	if err != nil {
		t.Fatal(err)
	}
	s := sim.New(trust)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return env{sim: s, srv: srv, creds: creds}
}

func (e env) clients(creds arca.Credentials) arca.Clients {
	return arca.NewClients(arca.Config{
		Env: "sim", CUIT: tramoCUIT, Timeout: 5 * time.Second, Creds: creds,
		Endpoints: arca.Endpoints{WSAA: e.srv.URL + sim.PathWSAA, Constancia: e.srv.URL + sim.PathConstancia, WSCDC: e.srv.URL + sim.PathWSCDC},
	})
}

func TestTRAIsSignedCMSWithArgentineTimes(t *testing.T) {
	trust, creds, err := sim.NewCredentials(tramoCUIT)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	tra, err := arca.TRA("wscdc", now)
	if err != nil {
		t.Fatal(err)
	}
	s := string(tra)
	for _, want := range []string{"<service>wscdc</service>", "<generationTime>2026-11-06T11:55:00-03:00</generationTime>",
		"<expirationTime>2026-11-06T12:10:00-03:00</expirationTime>", `<loginTicketRequest version="1.0">`} {
		if !strings.Contains(s, want) {
			t.Errorf("TRA lacks %s:\n%s", want, s)
		}
	}
	der, err := arca.SignTRA(tra, creds)
	if err != nil {
		t.Fatal(err)
	}
	p7, err := pkcs7.Parse(der)
	if err != nil {
		t.Fatal(err)
	}
	if err := p7.VerifyWithChain(trust); err != nil {
		t.Fatalf("CMS does not verify: %v", err)
	}
	if string(p7.Content) != s {
		t.Fatal("CMS does not carry the TRA attached")
	}
}

func TestOneLoginPerServiceUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	c := e.clients(e.creds)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := c.Constancia.GetPersona(context.Background(), sim.CUITMonotributo)
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := c.WSCDC.Constatar(context.Background(), fixtureComprobante())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if a, b := e.sim.Logins(arca.ServiceConstancia), e.sim.Logins(arca.ServiceWSCDC); a != 1 || b != 1 {
		t.Fatalf("loginCms calls: constancia %d, wscdc %d; want 1 each", a, b)
	}
}

// After a restart the gateway has no ticket but ARCA still has one out:
// WSAA answers coe.alreadyAuthenticated. The gateway must surface it and
// wait 60 seconds before asking again, as the manual requires.
func TestRestartWithTicketOutBacksOff(t *testing.T) {
	e := newEnv(t)
	if _, err := e.clients(e.creds).Constancia.GetPersona(context.Background(), sim.CUITMonotributo); err != nil {
		t.Fatal(err)
	}
	restarted := e.clients(e.creds) // new process, empty cache, same certificate
	_, err := restarted.Constancia.GetPersona(context.Background(), sim.CUITMonotributo)
	if !errors.Is(err, arca.FaultAlreadyAuthenticated) {
		t.Fatalf("restart: %v", err)
	}
	_, err = restarted.Constancia.GetPersona(context.Background(), sim.CUITMonotributo)
	if !errors.Is(err, arca.ErrBackoff) {
		t.Fatalf("second call during backoff: %v", err)
	}
	if n := e.sim.Logins(arca.ServiceConstancia); n != 2 {
		t.Fatalf("loginCms calls %d, want 2 (no retry inside the backoff)", n)
	}
}

func TestUntrustedCertificateIsRefused(t *testing.T) {
	e := newEnv(t)
	_, stranger, err := sim.NewCredentials(tramoCUIT) // same CUIT, another authority
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.clients(stranger).Constancia.GetPersona(context.Background(), sim.CUITMonotributo)
	if !errors.Is(err, &arca.FaultError{Code: "cms.sign.invalid"}) {
		t.Fatalf("untrusted cert: %v", err)
	}
}

func TestGetPersona(t *testing.T) {
	e := newEnv(t)
	c := e.clients(e.creds)
	ctx := context.Background()
	for cuit, want := range map[int64]arca.CondicionIVA{
		sim.CUITResponsableInscripto: arca.IVAResponsableInscripto,
		sim.CUITMonotributo:          arca.IVAMonotributo,
		sim.CUITSinIVA:               arca.IVANoDeterminada,
	} {
		p, err := c.Constancia.GetPersona(ctx, cuit)
		if err != nil {
			t.Fatalf("%d: %v", cuit, err)
		}
		if p.CondicionIVA != want || p.CUIT != cuit || p.Nombre == "" {
			t.Errorf("%d: %+v", cuit, p)
		}
	}
	p, _ := c.Constancia.GetPersona(ctx, sim.CUITMonotributo)
	if p.Nombre != "PEREZ (SIM) ANA" || p.CategoriaMonotributo != "B LOCACIONES DE SERVICIO" {
		t.Errorf("monotributo: %+v", p)
	}
	if _, err := c.Constancia.GetPersona(ctx, sim.CUITUnknown); !errors.Is(err, arca.ErrPersonaNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func fixtureComprobante() arca.Comprobante {
	v := sim.FixtureVoucher
	return arca.Comprobante{Modo: v.Modo, CUITEmisor: v.CUITEmisor, PuntoVenta: v.PtoVta, Tipo: v.Tipo, Numero: v.Numero,
		Fecha: v.Fecha, ImporteTotal: v.ImpTotal, CodAutorizacion: v.CodAutorizacion}
}

func TestConstatar(t *testing.T) {
	e := newEnv(t)
	c := e.clients(e.creds)
	ctx := context.Background()
	ok, err := c.WSCDC.Constatar(ctx, fixtureComprobante())
	if err != nil || !ok.Aprobado || ok.FechaProceso == "" {
		t.Fatalf("matching voucher: %+v %v", ok, err)
	}
	// One cent off: rejected with an observation, not an error.
	wrong := fixtureComprobante()
	wrong.ImporteTotal++
	r, err := c.WSCDC.Constatar(ctx, wrong)
	if err != nil || r.Aprobado || len(r.Observaciones) != 1 || r.Observaciones[0].Code != 9104 {
		t.Fatalf("amount off by a cent: %+v %v", r, err)
	}
}
