// Package userdatahttptest builds real mesh-style PKI material and real TLS
// listeners for admin-ui's userdata transport tests (ADR-0033 fixture list).
// It is imported only by tests.
package userdatahttptest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/httpclient"
)

// ProviderID is the provider's fixed identity (CN and service SAN).
const ProviderID = "userdata-local"

// CA is a test certificate authority standing in for core's mesh CA.
type CA struct {
	t    *testing.T
	dir  string
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	// File is the PEM CA bundle path.
	File string
}

var serial struct {
	sync.Mutex
	n int64
}

func nextSerial() *big.Int {
	serial.Lock()
	defer serial.Unlock()
	serial.n++
	return big.NewInt(time.Now().UnixNano() + serial.n)
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// NewCA creates a CA under a fresh temporary directory.
func NewCA(t *testing.T, name string) *CA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ca := &CA{t: t, dir: dir, cert: cert, key: key, File: filepath.Join(dir, "ca.crt")}
	writePEM(t, ca.File, "CERTIFICATE", der)
	return ca
}

// LeafOptions describes one module certificate.
type LeafOptions struct {
	CN       string
	DNS      []string
	IPs      []net.IP
	Usages   []x509.ExtKeyUsage // default: server and client auth, as core enrolls
	NotAfter time.Time          // default: one day ahead
}

// Leaf is an issued certificate with its files.
type Leaf struct {
	TLS      tls.Certificate
	CertFile string
	KeyFile  string
}

// Issue signs a leaf certificate.
func (ca *CA) Issue(opts LeafOptions) Leaf {
	t := ca.t
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	usages := opts.Usages
	if usages == nil {
		usages = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
	}
	notAfter := opts.NotAfter
	if notAfter.IsZero() {
		notAfter = time.Now().Add(24 * time.Hour)
	}
	tmpl := &x509.Certificate{
		SerialNumber: nextSerial(),
		Subject:      pkix.Name{CommonName: opts.CN},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
		DNSNames:     opts.DNS,
		IPAddresses:  opts.IPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	l := Leaf{CertFile: filepath.Join(dir, "module.crt"), KeyFile: filepath.Join(dir, "module.key")}
	writePEM(t, l.CertFile, "CERTIFICATE", der)
	writePEM(t, l.KeyFile, "EC PRIVATE KEY", keyDER)
	l.TLS, err = tls.LoadX509KeyPair(l.CertFile, l.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Loopback are the shared SANs core's enrollment adds to every module.
var Loopback = LeafOptions{DNS: []string{"localhost"}, IPs: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}

// Provider issues a correct userdata-local server certificate.
func (ca *CA) Provider() Leaf {
	return ca.Issue(LeafOptions{CN: ProviderID, DNS: []string{ProviderID, "localhost"}, IPs: Loopback.IPs})
}

// Module issues a module client certificate with the shared loopback SANs.
func (ca *CA) Module(cn string) Leaf {
	return ca.Issue(LeafOptions{CN: cn, DNS: append([]string{cn}, Loopback.DNS...), IPs: Loopback.IPs})
}

// ClientConfig is a secure (household) checked-client configuration for
// module using leaf and ca.
func ClientConfig(origin, module string, leaf Leaf, ca *CA, timeout time.Duration) httpclient.Config {
	return httpclient.Config{Origin: origin, ModuleID: module, Profile: "household",
		CertFile: leaf.CertFile, KeyFile: leaf.KeyFile, CAFile: ca.File, Timeout: timeout}
}

// VerifiedCN is the verified client certificate CN of r, or "".
func VerifiedCN(r *http.Request) string {
	if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.VerifiedChains[0]) > 0 {
		return r.TLS.VerifiedChains[0][0].Subject.CommonName
	}
	return ""
}

// Admit mirrors userdata-local v0.1.6's verified-CN admission table
// (internal/httptransport/admission.go, ADR-0033 §2) for the test provider.
func Admit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cn := VerifiedCN(r)
		ok := false
		switch r.URL.Path {
		case "/health":
			ok = (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
				(cn == "media-ui" || cn == "admin-ui" || cn == ProviderID || cn == "health-monitor")
		case "/api/parental-policy":
			ok = (cn == "media-ui" && r.Method == http.MethodGet) ||
				(cn == "admin-ui" && (r.Method == http.MethodGet || r.Method == http.MethodPut))
		case "/api/userdata":
			ok = (cn == "media-ui" || cn == "admin-ui") && (r.Method == http.MethodGet || r.Method == http.MethodPut)
		}
		if !ok || r.URL.RawPath != "" {
			ModuleForbidden(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ModuleForbidden writes the provider's exact module-admission refusal.
func ModuleForbidden(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-MuxCore-Error-Code", "userdata.module_forbidden")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": "userdata.module_forbidden"})
}

// StartTLS serves h over real mTLS on loopback with the server certificate
// leaf, requiring a client certificate from clientCA (the provider's
// RequireAndVerifyClientCert with an explicit CA pool).
func StartTLS(t *testing.T, leaf Leaf, clientCA *CA, h http.Handler) *httptest.Server {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(clientCA.cert)
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{leaf.TLS},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// StartServerOnlyTLS serves h over TLS without requiring a client
// certificate: an impostor that would accept anyone's request.
func StartServerOnlyTLS(t *testing.T, leaf Leaf, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{leaf.TLS}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// Recorder counts requests that reached a handler and keeps every
// Authorization header it saw, so a test can prove a bearer never left.
type Recorder struct {
	mu    sync.Mutex
	n     int
	auths []string
}

// Wrap returns a handler that records and then calls next (or 200 {}).
func (rec *Recorder) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.n++
		rec.auths = append(rec.auths, r.Header.Values("Authorization")...)
		rec.mu.Unlock()
		if next != nil {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
}

// Count is the number of requests that reached the handler.
func (rec *Recorder) Count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.n
}

// Auths are the Authorization headers that reached the handler.
func (rec *Recorder) Auths() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]string(nil), rec.auths...)
}
