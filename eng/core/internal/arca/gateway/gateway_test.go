package gateway_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	arcav1 "github.com/Tramo-Labs/hq/eng/core/gen/arca/v1"
	"github.com/Tramo-Labs/hq/eng/core/internal/arca"
	"github.com/Tramo-Labs/hq/eng/core/internal/arca/gateway"
	"github.com/Tramo-Labs/hq/eng/core/internal/arca/sim"
)

// TestGatewayAgainstSimEndToEnd starts the gateway exactly as
// cmd/arca-gateway does with ARCA_ENV=sim (config from the environment,
// simulator in process, real TCP listener) and talks to it as core will:
// gRPC over the network.
func TestGatewayAgainstSimEndToEnd(t *testing.T) {
	t.Setenv("ARCA_ENV", "sim")
	t.Setenv("ARCA_CUIT", "")
	cfg, err := arca.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gateway.Run(ctx, cfg, lis) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("gateway stopped with %v", err)
		}
	})

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	call, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()

	h, err := healthpb.NewHealthClient(conn).Check(call, &healthpb.HealthCheckRequest{})
	if err != nil || h.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health: %v %v", h, err)
	}
	c := arcav1.NewArcaServiceClient(conn)

	t.Run("persona responsable inscripto", func(t *testing.T) {
		p, err := c.GetPersona(call, &arcav1.GetPersonaRequest{Cuit: sim.CUITResponsableInscripto})
		if err != nil {
			t.Fatal(err)
		}
		if p.GetCondicionIva() != arcav1.CondicionIva_CONDICION_IVA_RESPONSABLE_INSCRIPTO || p.GetNombre() != "MOLINO DEL SUR SA (SIM)" ||
			p.GetTipoPersona() != "JURIDICA" || p.GetDomicilioFiscal().GetLocalidad() != "RIO CUARTO" || len(p.GetImpuestos()) != 2 {
			t.Fatalf("%v", p)
		}
		t.Logf("GetPersona(%d) -> %s, %s, %s, %d impuestos", p.GetCuit(), p.GetNombre(), p.GetCondicionIva(), p.GetDomicilioFiscal().GetLocalidad(), len(p.GetImpuestos()))
	})
	t.Run("persona monotributo", func(t *testing.T) {
		p, err := c.GetPersona(call, &arcav1.GetPersonaRequest{Cuit: sim.CUITMonotributo})
		if err != nil || p.GetCondicionIva() != arcav1.CondicionIva_CONDICION_IVA_MONOTRIBUTO || p.GetCategoriaMonotributo() == "" {
			t.Fatalf("%v %v", p, err)
		}
		t.Logf("GetPersona(%d) -> %s, %s", p.GetCuit(), p.GetCondicionIva(), p.GetCategoriaMonotributo())
	})
	t.Run("persona not found", func(t *testing.T) {
		_, err := c.GetPersona(call, &arcav1.GetPersonaRequest{Cuit: sim.CUITUnknown})
		if status.Code(err) != codes.NotFound {
			t.Fatalf("got %v", err)
		}
		t.Logf("GetPersona(%d) -> %s", sim.CUITUnknown, status.Code(err))
	})
	t.Run("invalid cuit never reaches ARCA", func(t *testing.T) {
		_, err := c.GetPersona(call, &arcav1.GetPersonaRequest{Cuit: 20000000029})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("got %v", err)
		}
	})

	v := sim.FixtureVoucher
	req := func() *arcav1.ConstatarComprobanteRequest {
		return &arcav1.ConstatarComprobanteRequest{
			Modo: arcav1.ModoAutorizacion_MODO_AUTORIZACION_CAE, CuitEmisor: v.CUITEmisor, PuntoVenta: v.PtoVta,
			TipoComprobante: v.Tipo, Numero: v.Numero, Fecha: v.Fecha, ImporteTotalCents: int64(v.ImpTotal),
			CodigoAutorizacion: v.CodAutorizacion,
		}
	}
	t.Run("constatar approved", func(t *testing.T) {
		r, err := c.ConstatarComprobante(call, req())
		if err != nil || r.GetResultado() != arcav1.ResultadoConstatacion_RESULTADO_CONSTATACION_APROBADO || r.GetFechaProceso() == "" {
			t.Fatalf("%v %v", r, err)
		}
		t.Logf("ConstatarComprobante(CAE %s, %d cents) -> %s", v.CodAutorizacion, v.ImpTotal, r.GetResultado())
	})
	t.Run("constatar wrong CAE rejected", func(t *testing.T) {
		bad := req()
		bad.CodigoAutorizacion = "76123456789013"
		r, err := c.ConstatarComprobante(call, bad)
		if err != nil || r.GetResultado() != arcav1.ResultadoConstatacion_RESULTADO_CONSTATACION_RECHAZADO || len(r.GetObservaciones()) != 1 {
			t.Fatalf("%v %v", r, err)
		}
		t.Logf("ConstatarComprobante(wrong CAE) -> %s, obs %d %q", r.GetResultado(), r.GetObservaciones()[0].GetCode(), r.GetObservaciones()[0].GetMsg())
	})
	t.Run("constatar bad input", func(t *testing.T) {
		for name, mut := range map[string]func(*arcav1.ConstatarComprobanteRequest){
			"no modo":     func(r *arcav1.ConstatarComprobanteRequest) { r.Modo = 0 },
			"bad fecha":   func(r *arcav1.ConstatarComprobanteRequest) { r.Fecha = "2026-11-03" },
			"short CAE":   func(r *arcav1.ConstatarComprobanteRequest) { r.CodigoAutorizacion = "123" },
			"negative":    func(r *arcav1.ConstatarComprobanteRequest) { r.ImporteTotalCents = -1 },
			"bad emisor":  func(r *arcav1.ConstatarComprobanteRequest) { r.CuitEmisor = 30000000016 },
			"zero numero": func(r *arcav1.ConstatarComprobanteRequest) { r.Numero = 0 },
		} {
			r := req()
			mut(r)
			if _, err := c.ConstatarComprobante(call, r); status.Code(err) != codes.InvalidArgument {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
}

func TestLoadConfigOutsideSimNeedsRealCredentials(t *testing.T) {
	t.Setenv("ARCA_ENV", "homologacion")
	t.Setenv("ARCA_CUIT", "30000000023")
	t.Setenv("ARCA_CERT", "")
	t.Setenv("ARCA_KEY", "")
	if _, err := arca.LoadConfig(); err == nil {
		t.Fatal("homologacion without certificate accepted")
	}
	t.Setenv("ARCA_ENV", "produccion")
	t.Setenv("ARCA_WSAA_URL", "http://wsaa.example")
	if _, err := arca.LoadConfig(); err == nil {
		t.Fatal("plain http endpoint accepted")
	}
	t.Setenv("ARCA_ENV", "testing")
	if _, err := arca.LoadConfig(); err == nil {
		t.Fatal("unknown ARCA_ENV accepted")
	}
}
