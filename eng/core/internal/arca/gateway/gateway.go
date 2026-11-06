// Package gateway is the arca-gateway process: the gRPC service of
// proto/arca/v1 on top of Tramo's own ARCA clients. It is the only process
// that holds the certificate private key (ARCA_KEY).
//
// With ARCA_ENV=sim it starts the simulator in process on loopback with a
// throwaway certificate, so core, CI and local work never need ARCA.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	arcav1 "github.com/Tramo-Labs/hq/eng/core/gen/arca/v1"
	"github.com/Tramo-Labs/hq/eng/core/internal/arca"
	"github.com/Tramo-Labs/hq/eng/core/internal/arca/sim"
	"github.com/Tramo-Labs/hq/eng/core/internal/cuit"
	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Server implements arcav1.ArcaServiceServer.
type Server struct {
	arcav1.UnimplementedArcaServiceServer
	clients arca.Clients
}

// NewServer wraps the ARCA clients.
func NewServer(c arca.Clients) *Server { return &Server{clients: c} }

// GetPersona reads the registration record of a CUIT.
func (s *Server) GetPersona(ctx context.Context, req *arcav1.GetPersonaRequest) (*arcav1.GetPersonaResponse, error) {
	if !cuit.Valid(req.GetCuit()) {
		return nil, status.Error(codes.InvalidArgument, "cuit must be 11 digits with a valid check digit")
	}
	p, err := s.clients.Constancia.GetPersona(ctx, req.GetCuit())
	if err != nil {
		return nil, toStatus(err)
	}
	out := &arcav1.GetPersonaResponse{
		Cuit: p.CUIT, TipoPersona: p.TipoPersona, Nombre: p.Nombre, EstadoClave: p.EstadoClave,
		CondicionIva:         condicion(p.CondicionIVA),
		CategoriaMonotributo: p.CategoriaMonotributo,
		DomicilioFiscal: &arcav1.Domicilio{
			Direccion: p.Domicilio.Direccion, Localidad: p.Domicilio.Localidad,
			CodPostal: p.Domicilio.CodPostal, Provincia: p.Domicilio.Provincia,
		},
		Avisos:     p.Avisos,
		Consultado: p.Consultado,
	}
	for _, i := range p.Impuestos {
		out.Impuestos = append(out.Impuestos, &arcav1.Impuesto{Id: i.ID, Descripcion: i.Descripcion, Estado: i.Estado, Periodo: i.Periodo})
	}
	return out, nil
}

var (
	reFecha  = regexp.MustCompile(`^20\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])$`)
	reCodAut = regexp.MustCompile(`^\d{14}$`)
)

// ConstatarComprobante checks a voucher with WSCDC.
func (s *Server) ConstatarComprobante(ctx context.Context, req *arcav1.ConstatarComprobanteRequest) (*arcav1.ConstatarComprobanteResponse, error) {
	modo := map[arcav1.ModoAutorizacion]string{
		arcav1.ModoAutorizacion_MODO_AUTORIZACION_CAE:  "CAE",
		arcav1.ModoAutorizacion_MODO_AUTORIZACION_CAEA: "CAEA",
		arcav1.ModoAutorizacion_MODO_AUTORIZACION_CAI:  "CAI",
	}[req.GetModo()]
	switch {
	case modo == "":
		return nil, status.Error(codes.InvalidArgument, "modo is required")
	case !cuit.Valid(req.GetCuitEmisor()):
		return nil, status.Error(codes.InvalidArgument, "cuit_emisor must be a valid CUIT")
	case req.GetPuntoVenta() <= 0 || req.GetPuntoVenta() > 99999:
		return nil, status.Error(codes.InvalidArgument, "punto_venta out of range")
	case req.GetTipoComprobante() <= 0 || req.GetTipoComprobante() > 999:
		return nil, status.Error(codes.InvalidArgument, "tipo_comprobante out of range")
	case req.GetNumero() <= 0 || req.GetNumero() > 99999999:
		return nil, status.Error(codes.InvalidArgument, "numero out of range")
	case !reFecha.MatchString(req.GetFecha()):
		return nil, status.Error(codes.InvalidArgument, "fecha must be YYYYMMDD")
	case req.GetImporteTotalCents() < 0:
		return nil, status.Error(codes.InvalidArgument, "importe_total_cents must not be negative")
	case !reCodAut.MatchString(req.GetCodigoAutorizacion()):
		return nil, status.Error(codes.InvalidArgument, "codigo_autorizacion must be 14 digits")
	}
	r, err := s.clients.WSCDC.Constatar(ctx, arca.Comprobante{
		Modo: modo, CUITEmisor: req.GetCuitEmisor(), PuntoVenta: req.GetPuntoVenta(), Tipo: req.GetTipoComprobante(),
		Numero: req.GetNumero(), Fecha: req.GetFecha(), ImporteTotal: money.Amount(req.GetImporteTotalCents()),
		CodAutorizacion: req.GetCodigoAutorizacion(), DocTipoReceptor: req.GetDocTipoReceptor(), DocNroReceptor: req.GetDocNroReceptor(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	out := &arcav1.ConstatarComprobanteResponse{
		Resultado:    arcav1.ResultadoConstatacion_RESULTADO_CONSTATACION_RECHAZADO,
		FechaProceso: r.FechaProceso,
	}
	if r.Aprobado {
		out.Resultado = arcav1.ResultadoConstatacion_RESULTADO_CONSTATACION_APROBADO
	}
	for _, o := range r.Observaciones {
		out.Observaciones = append(out.Observaciones, &arcav1.Observacion{Code: o.Code, Msg: o.Msg})
	}
	for _, o := range r.Errores {
		out.Errores = append(out.Errores, &arcav1.Observacion{Code: o.Code, Msg: o.Msg})
	}
	return out, nil
}

func condicion(c arca.CondicionIVA) arcav1.CondicionIva {
	switch c {
	case arca.IVAResponsableInscripto:
		return arcav1.CondicionIva_CONDICION_IVA_RESPONSABLE_INSCRIPTO
	case arca.IVAMonotributo:
		return arcav1.CondicionIva_CONDICION_IVA_MONOTRIBUTO
	}
	return arcav1.CondicionIva_CONDICION_IVA_NO_DETERMINADA
}

// toStatus maps client errors to gRPC codes. Messages are ARCA's own text
// (never the key, the token or the sign).
func toStatus(err error) error {
	var fault *arca.FaultError
	switch {
	case errors.Is(err, arca.ErrPersonaNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, arca.ErrBackoff), errors.Is(err, arca.ErrUnavailable),
		errors.Is(err, arca.FaultAlreadyAuthenticated), errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.Unavailable, err.Error())
	case errors.As(err, &fault):
		return status.Error(codes.Unavailable, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

// Run serves the gateway on lis until ctx ends. For ARCA_ENV=sim it starts
// the simulator on loopback first.
func Run(ctx context.Context, cfg arca.Config, lis net.Listener) error {
	if cfg.Env == "sim" {
		stop, err := startSim(&cfg)
		if err != nil {
			return err
		}
		defer stop()
	}
	srv := grpc.NewServer()
	arcav1.RegisterArcaServiceServer(srv, NewServer(arca.NewClients(cfg)))
	hs := health.NewServer()
	healthpb.RegisterHealthServer(srv, hs)
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(lis) }()
	slog.Info("arca-gateway serving", "addr", lis.Addr().String(), "arca_env", cfg.Env, "cuit", cfg.CUIT)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	done := make(chan struct{})
	go func() { srv.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		srv.Stop()
	}
	return nil
}

// startSim runs the simulator on a loopback port and points cfg at it with
// a throwaway certificate for cfg.CUIT.
func startSim(cfg *arca.Config) (func(), error) {
	trust, creds, err := sim.NewCredentials(cfg.CUIT)
	if err != nil {
		return nil, err
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	hs := &http.Server{Handler: sim.New(trust).Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.Serve(lis) }()
	base := fmt.Sprintf("http://%s", lis.Addr())
	cfg.Endpoints = arca.Endpoints{WSAA: base + sim.PathWSAA, Constancia: base + sim.PathConstancia, WSCDC: base + sim.PathWSCDC}
	cfg.Creds = creds
	slog.Warn("ARCA_ENV=sim: answering from the local simulator with fictitious data", "sim", base)
	return func() { _ = hs.Close() }, nil
}
