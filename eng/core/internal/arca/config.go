package arca

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/cuit"
)

// Endpoints of the three services.
type Endpoints struct {
	WSAA, Constancia, WSCDC string
}

// Official endpoints (eng/arca/manuales: WSAA manual and spec, constancia
// v4.1 section 2.5, WSCDC section 2.1).
var (
	Homologacion = Endpoints{
		WSAA:       "https://wsaahomo.afip.gov.ar/ws/services/LoginCms",
		Constancia: "https://awshomo.arca.gob.ar/sr-padron/webservices/personaServiceA5",
		WSCDC:      "https://wswhomo.afip.gob.ar/WSCDC/service.asmx",
	}
	Produccion = Endpoints{
		WSAA:       "https://wsaa.afip.gov.ar/ws/services/LoginCms",
		Constancia: "https://aws.arca.gob.ar/sr-padron/webservices/personaServiceA5",
		WSCDC:      "https://servicios1.arca.gob.ar/WSCDC/service.asmx",
	}
)

// Config of the arca-gateway. Only cmd/arca-gateway reads it: ARCA_KEY must
// never be set for any other process (a test enforces that no other package
// mentions it).
type Config struct {
	Env       string // sim, homologacion, produccion
	CUIT      int64  // Tramo's CUIT: serialNumber of the certificate, cuitRepresentada
	Endpoints Endpoints
	Timeout   time.Duration
	Creds     Credentials // empty for sim: the simulator issues its own
}

// LoadConfig reads ARCA_ENV, ARCA_CUIT, ARCA_CERT and ARCA_KEY (PEM text,
// from the environment only) and optional ARCA_*_URL overrides.
func LoadConfig() (Config, error) {
	c := Config{Env: getenv("ARCA_ENV", "sim"), Timeout: 30 * time.Second}
	switch c.Env {
	case "sim":
		c.CUIT = 30000000023 // fictitious, valid check digit
		if v := os.Getenv("ARCA_CUIT"); v != "" {
			n, err := cuit.Parse(v)
			if err != nil {
				return c, fmt.Errorf("ARCA_CUIT: %w", err)
			}
			c.CUIT = n
		}
		return c, nil
	case "homologacion":
		c.Endpoints = Homologacion
	case "produccion":
		c.Endpoints = Produccion
	default:
		return c, fmt.Errorf("ARCA_ENV %q is not one of sim, homologacion, produccion", c.Env)
	}
	n, err := cuit.Parse(os.Getenv("ARCA_CUIT"))
	if err != nil {
		return c, errors.New("ARCA_CUIT is required (11 digits, valid check digit)")
	}
	c.CUIT = n
	for name, dst := range map[string]*string{
		"ARCA_WSAA_URL": &c.Endpoints.WSAA, "ARCA_CONSTANCIA_URL": &c.Endpoints.Constancia, "ARCA_WSCDC_URL": &c.Endpoints.WSCDC,
	} {
		if v := os.Getenv(name); v != "" {
			*dst = v
		}
	}
	for _, u := range []string{c.Endpoints.WSAA, c.Endpoints.Constancia, c.Endpoints.WSCDC} {
		if p, err := url.Parse(u); err != nil || p.Scheme != "https" {
			return c, fmt.Errorf("ARCA endpoint %q must be https", u)
		}
	}
	if v := os.Getenv("ARCA_TIMEOUT_SECONDS"); v != "" {
		s, err := strconv.Atoi(v)
		if err != nil || s <= 0 {
			return c, errors.New("ARCA_TIMEOUT_SECONDS must be a positive integer")
		}
		c.Timeout = time.Duration(s) * time.Second
	}
	c.Creds, err = ParseCredentials([]byte(os.Getenv("ARCA_CERT")), []byte(os.Getenv("ARCA_KEY")))
	if err != nil {
		return c, err
	}
	if err := checkCertCUIT(c.Creds.Cert, c.CUIT); err != nil {
		return c, err
	}
	return c, nil
}

// ParseCredentials reads a PEM certificate and a PEM private key (PKCS#8 or
// PKCS#1) and checks that they belong together. Errors never echo the key.
func ParseCredentials(certPEM, keyPEM []byte) (Credentials, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil || cb.Type != "CERTIFICATE" {
		return Credentials{}, errors.New("ARCA_CERT must be a PEM certificate")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return Credentials{}, errors.New("ARCA_CERT does not parse")
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return Credentials{}, errors.New("ARCA_KEY must be a PEM private key")
	}
	var key any
	switch kb.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(kb.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(kb.Bytes)
	default:
		err = errors.New("unsupported")
	}
	if err != nil {
		return Credentials{}, errors.New("ARCA_KEY does not parse (PKCS#8 or PKCS#1 PEM expected)")
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return Credentials{}, errors.New("ARCA_KEY is not a signing key")
	}
	type equaler interface{ Equal(crypto.PublicKey) bool }
	if pub, ok := signer.Public().(equaler); !ok || !pub.Equal(cert.PublicKey) {
		return Credentials{}, errors.New("ARCA_KEY does not match ARCA_CERT")
	}
	return Credentials{Cert: cert, Key: signer}, nil
}

// checkCertCUIT verifies the certificate is Tramo's: serialNumber "CUIT n".
func checkCertCUIT(cert *x509.Certificate, n int64) error {
	if cert.Subject.SerialNumber != "CUIT "+strconv.FormatInt(n, 10) {
		return fmt.Errorf("certificate serialNumber %q does not match ARCA_CUIT", cert.Subject.SerialNumber)
	}
	return nil
}

// Clients are the ARCA services the gateway exposes.
type Clients struct {
	Tickets    *WSAA
	Constancia *Constancia
	WSCDC      *WSCDC
}

// NewClients wires the three clients for c.
func NewClients(c Config) Clients {
	s := newSOAPClient(c.Timeout)
	w := NewWSAA(c.Endpoints.WSAA, c.Creds, c.Timeout)
	return Clients{
		Tickets:    w,
		Constancia: NewConstancia(c.Endpoints.Constancia, w, c.CUIT, s),
		WSCDC:      NewWSCDC(c.Endpoints.WSCDC, w, c.CUIT, s),
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
