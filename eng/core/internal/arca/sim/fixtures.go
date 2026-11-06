package sim

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/arca"
)

// Fixture CUITs. Synthetic (all zeros pattern, valid check digits, checked
// in internal/cuit tests); never use a real company's CUIT here.
const (
	CUITResponsableInscripto int64 = 30000000015 // juridica, IVA active
	CUITMonotributo          int64 = 20000000060 // fisica, monotributo
	CUITSinIVA               int64 = 27000000006 // fisica, only income tax
	CUITUnknown              int64 = 20000000028 // valid digit, not in the fixtures
)

// DefaultPersonas are the people the simulator knows.
func DefaultPersonas() map[int64]Persona {
	return map[int64]Persona{
		CUITResponsableInscripto: {
			CUIT: CUITResponsableInscripto, TipoPersona: "JURIDICA", EstadoClave: "ACTIVO",
			RazonSocial: "MOLINO DEL SUR SA (SIM)",
			Direccion:   "RUTA 8 KM 600", Localidad: "RIO CUARTO", CodPostal: "5800", Provincia: "CORDOBA",
			RegimenGeneral: []Impuesto{
				{ID: 30, Descripcion: "IVA", Estado: "AC", Periodo: "200101"},
				{ID: 10, Descripcion: "GANANCIAS SOCIEDADES", Estado: "AC", Periodo: "200101"},
			},
		},
		CUITMonotributo: {
			CUIT: CUITMonotributo, TipoPersona: "FISICA", EstadoClave: "ACTIVO",
			Apellido: "PEREZ (SIM)", Nombre: "ANA",
			Direccion: "SAN MARTIN 100", Localidad: "CORDOBA", CodPostal: "5000", Provincia: "CORDOBA",
			Monotributo:          []Impuesto{{ID: 20, Descripcion: "MONOTRIBUTO", Estado: "AC", Periodo: "201803"}},
			CategoriaMonotributo: "B LOCACIONES DE SERVICIO",
		},
		CUITSinIVA: {
			CUIT: CUITSinIVA, TipoPersona: "FISICA", EstadoClave: "ACTIVO",
			Apellido: "GOMEZ (SIM)", Nombre: "LUIS",
			RegimenGeneral: []Impuesto{{ID: 11, Descripcion: "GANANCIAS PERSONAS FISICAS", Estado: "AC", Periodo: "199203"}},
		},
	}
}

// FixtureVoucher is the one authorized voucher the simulator knows.
var FixtureVoucher = Voucher{
	Modo: "CAE", CUITEmisor: CUITResponsableInscripto, PtoVta: 1, Tipo: 1, Numero: 2,
	Fecha: "20261103", ImpTotal: 12100050, CodAutorizacion: "76123456789012",
}

// DefaultVouchers are the authorized vouchers the simulator knows.
func DefaultVouchers() []Voucher { return []Voucher{FixtureVoucher} }

// NewCredentials creates a throwaway certificate authority and a gateway
// certificate for cuit shaped like ARCA's (serialNumber "CUIT n"). The
// returned pool trusts only that authority. For ARCA_ENV=sim and tests:
// nothing is written to disk.
func NewCredentials(cuit int64) (*x509.CertPool, arca.Credentials, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, arca.Credentials{}, err
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Tramo ARCA simulator CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, arca.Credentials{}, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, arca.Credentials{}, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, arca.Credentials{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, arca.Credentials{}, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "tramo-gateway", Organization: []string{"Tramo (sim)"}, Country: []string{"AR"},
			SerialNumber: fmt.Sprintf("CUIT %d", cuit),
		},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, arca.Credentials{}, err
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, arca.Credentials{}, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pool, arca.Credentials{Cert: leaf, Key: key}, nil
}
