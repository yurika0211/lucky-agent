package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newPairingServer() *Server {
	return &Server{pairing: newPairingStore()}
}

func TestPairingKeyAcceptedUntilExpiry(t *testing.T) {
	s := newPairingServer()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s.pairing.now = func() time.Time { return now }

	issued, err := s.IssuePairingKey("http://192.168.1.8:9090", time.Hour, "desk")
	if err != nil {
		t.Fatal(err)
	}
	if !s.pairingKeyValid(issued.Key) {
		t.Fatal("fresh pairing key should be accepted")
	}

	now = now.Add(time.Hour + time.Second)
	if s.pairingKeyValid(issued.Key) {
		t.Fatal("expired pairing key should be rejected")
	}
}

func TestPairingKeyIsProcessLocal(t *testing.T) {
	first := newPairingServer()
	issued, err := first.IssuePairingKey("http://10.0.0.8:9090", DefaultPairingTTL, "")
	if err != nil {
		t.Fatal(err)
	}
	restarted := newPairingServer()
	if restarted.pairingKeyValid(issued.Key) {
		t.Fatal("a restarted server must not keep the previous pairing key")
	}
}

func TestPairingAuthAllowsLANWhenOnlyPairingKeyExists(t *testing.T) {
	s := newPairingServer()
	issued, err := s.IssuePairingKey("http://192.168.1.8:9090", time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	handler := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.RemoteAddr = "192.168.1.20:1234"
	req.Header.Set("X-API-Key", issued.Key)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pairing key should authorize a LAN client, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.RemoteAddr = "192.168.1.20:1234"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("LAN client without a key should stay rejected, got %d", w.Code)
	}
}

func TestPairingPayloadCarriesURLAndToken(t *testing.T) {
	key := PairingKey{
		Key:       "lp_test",
		URL:       "http://192.168.1.8:9090",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		Label:     "desk",
	}
	text, err := key.QRText()
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`"v":1`, key.URL, key.Key, `"ttl_hours":24`} {
		if !strings.Contains(text, part) {
			t.Fatalf("qr text missing %q: %s", part, text)
		}
	}
}

func TestLanAdvertiseURLUsesConcreteHost(t *testing.T) {
	got, err := lanAdvertiseURL("192.168.1.8:9090")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://192.168.1.8:9090" {
		t.Fatalf("got %s", got)
	}
}

func TestLanAdvertiseURLFindsIPv4(t *testing.T) {
	got, err := lanAdvertiseURL("0.0.0.0:9090")
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(strings.TrimPrefix(got, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil || ip.IsLoopback() {
		t.Fatalf("expected a LAN IPv4 URL, got %s", got)
	}
}
