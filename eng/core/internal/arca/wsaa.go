package arca

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/smallstep/pkcs7"
)

const wsaaNS = "http://wsaa.view.sua.dvadac.desein.afip.gov"

// Argentina time (GMT-3, no DST) for TRA timestamps, as the WSAA manual asks.
var argentina = time.FixedZone("ART", -3*60*60)

// Credentials are Tramo's certificate and private key. They exist only in
// the arca-gateway process.
type Credentials struct {
	Cert *x509.Certificate
	Key  crypto.Signer
}

// Ticket is a WSAA access ticket (TA): valid 12 hours for one service.
type Ticket struct {
	Token, Sign string
	Expires     time.Time
}

// ErrBackoff means the gateway is waiting before asking WSAA again, as the
// manual requires after an error (60 seconds) or while ARCA says a ticket
// for this service is already out (coe.alreadyAuthenticated).
var ErrBackoff = errors.New("arca: waiting before asking WSAA again")

// FaultAlreadyAuthenticated is the WSAA fault for "you already hold a valid
// TA for this service".
var FaultAlreadyAuthenticated = &FaultError{Code: "coe.alreadyAuthenticated"}

// WSAA obtains and caches access tickets. One ticket per service is shared
// by every caller; at most one loginCms per service is in flight.
type WSAA struct {
	soap   soapClient
	url    string
	creds  Credentials
	now    func() time.Time
	margin time.Duration // renew when less than this is left
	retry  time.Duration // wait after a failed login

	mu      sync.Mutex
	entries map[string]*ticketEntry
}

type ticketEntry struct {
	mu           sync.Mutex
	ticket       Ticket
	blockedUntil time.Time
	lastErr      error
}

// NewWSAA builds the ticket client for the WSAA endpoint url.
func NewWSAA(url string, creds Credentials, timeout time.Duration) *WSAA {
	return &WSAA{
		soap: newSOAPClient(timeout), url: url, creds: creds, now: time.Now,
		margin: 10 * time.Minute, retry: 60 * time.Second,
		entries: map[string]*ticketEntry{},
	}
}

func (w *WSAA) entry(service string) *ticketEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	e, ok := w.entries[service]
	if !ok {
		e = &ticketEntry{}
		w.entries[service] = e
	}
	return e
}

// Ticket returns a valid TA for service, asking WSAA only when needed.
func (w *WSAA) Ticket(ctx context.Context, service string) (Ticket, error) {
	e := w.entry(service)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := w.now()
	if e.ticket.Token != "" && now.Add(w.margin).Before(e.ticket.Expires) {
		return e.ticket, nil
	}
	if now.Before(e.blockedUntil) {
		if e.ticket.Token != "" && now.Before(e.ticket.Expires) {
			return e.ticket, nil
		}
		return Ticket{}, fmt.Errorf("%w until %s: %v", ErrBackoff, e.blockedUntil.Format(time.RFC3339), e.lastErr)
	}
	t, err := w.login(ctx, service)
	if err != nil {
		// Keep using a ticket that is still valid (renewal inside the margin
		// may legitimately get coe.alreadyAuthenticated).
		e.lastErr, e.blockedUntil = err, now.Add(w.retry)
		if e.ticket.Token != "" && now.Before(e.ticket.Expires) {
			return e.ticket, nil
		}
		return Ticket{}, err
	}
	e.ticket, e.lastErr, e.blockedUntil = t, nil, time.Time{}
	return t, nil
}

type loginTicketRequest struct {
	XMLName xml.Name `xml:"loginTicketRequest"`
	Version string   `xml:"version,attr"`
	Header  struct {
		UniqueID       uint32 `xml:"uniqueId"`
		GenerationTime string `xml:"generationTime"`
		ExpirationTime string `xml:"expirationTime"`
	} `xml:"header"`
	Service string `xml:"service"`
}

type loginTicketResponse struct {
	Header struct {
		GenerationTime string `xml:"generationTime"`
		ExpirationTime string `xml:"expirationTime"`
	} `xml:"header"`
	Credentials struct {
		Token string `xml:"token"`
		Sign  string `xml:"sign"`
	} `xml:"credentials"`
}

// TRA builds the login ticket request for service: generated a few minutes
// in the past to absorb clock skew, expiring ten minutes ahead.
func TRA(service string, now time.Time) ([]byte, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	r := loginTicketRequest{Version: "1.0", Service: service}
	r.Header.UniqueID = binary.BigEndian.Uint32(b[:])
	r.Header.GenerationTime = now.Add(-5 * time.Minute).In(argentina).Format(time.RFC3339)
	r.Header.ExpirationTime = now.Add(10 * time.Minute).In(argentina).Format(time.RFC3339)
	out, err := xml.Marshal(r)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

// SignTRA wraps tra in a CMS SignedData with the content attached, signed
// with SHA-256 and RSA (what `openssl cms -sign -nodetach` produces in the
// WSAA developer manual).
func SignTRA(tra []byte, c Credentials) ([]byte, error) {
	if c.Cert == nil || c.Key == nil {
		// Fail as an error, not as a nil dereference inside the CMS library.
		return nil, errors.New("arca: no certificate or key to sign the TRA")
	}
	sd, err := pkcs7.NewSignedData(tra)
	if err != nil {
		return nil, err
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSigner(c.Cert, c.Key, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, fmt.Errorf("arca: cms signer: %w", err)
	}
	return sd.Finish()
}

func (w *WSAA) login(ctx context.Context, service string) (Ticket, error) {
	tra, err := TRA(service, w.now())
	if err != nil {
		return Ticket{}, err
	}
	cms, err := SignTRA(tra, w.creds)
	if err != nil {
		return Ticket{}, err
	}
	req := struct {
		XMLName xml.Name `xml:"loginCms"`
		NS      string   `xml:"xmlns,attr"`
		In0     string   `xml:"in0"`
	}{NS: wsaaNS, In0: base64.StdEncoding.EncodeToString(cms)}
	var res struct {
		Return string `xml:"loginCmsReturn"`
	}
	if err := w.soap.call(ctx, w.url, "", req, &res); err != nil {
		return Ticket{}, err
	}
	var ltr loginTicketResponse
	if err := xml.Unmarshal([]byte(strings.TrimSpace(res.Return)), &ltr); err != nil {
		return Ticket{}, fmt.Errorf("arca: loginTicketResponse: %w", err)
	}
	exp, err := time.Parse(time.RFC3339, strings.TrimSpace(ltr.Header.ExpirationTime))
	if err != nil {
		return Ticket{}, fmt.Errorf("arca: TA expirationTime: %w", err)
	}
	if ltr.Credentials.Token == "" || ltr.Credentials.Sign == "" || !exp.After(w.now()) {
		return Ticket{}, errors.New("arca: WSAA returned an empty or expired ticket")
	}
	return Ticket{Token: ltr.Credentials.Token, Sign: ltr.Credentials.Sign, Expires: exp}, nil
}
