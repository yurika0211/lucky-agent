package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultPairingTTL is the lifetime of a QR pairing key.
	// A process restart drops every key before this deadline.
	DefaultPairingTTL = 24 * time.Hour
	pairingKeyBytes   = 24
)

// PairingKey is a process-local API key issued by `lh qr`.
// It is never written to config.json. Restarting the API drops it.
type PairingKey struct {
	Key       string
	URL       string
	ExpiresAt time.Time
	Label     string
}

type pairingStore struct {
	mu   sync.Mutex
	keys []PairingKey
	now  func() time.Time
}

func newPairingStore() *pairingStore {
	return &pairingStore{now: time.Now}
}

// IssuePairingKey keeps a new key in memory until expiresAt.
// The key is accepted by auth even when server.api_keys is empty.
func (s *Server) IssuePairingKey(apiURL string, ttl time.Duration, label string) (PairingKey, error) {
	if s == nil || s.pairing == nil {
		return PairingKey{}, errors.New("pairing store unavailable")
	}
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if apiURL == "" {
		return PairingKey{}, errors.New("pairing url is empty")
	}
	if _, err := url.ParseRequestURI(apiURL); err != nil || !strings.Contains(apiURL, "://") {
		return PairingKey{}, fmt.Errorf("pairing url is invalid: %s", apiURL)
	}
	if ttl <= 0 {
		ttl = DefaultPairingTTL
	}
	raw := make([]byte, pairingKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return PairingKey{}, err
	}
	key := "lp_" + base64.RawURLEncoding.EncodeToString(raw)
	issued := PairingKey{
		Key:       key,
		URL:       apiURL,
		ExpiresAt: s.pairing.now().Add(ttl),
		Label:     strings.TrimSpace(label),
	}
	s.pairing.mu.Lock()
	s.pairing.keys = append(s.pairing.keys, issued)
	s.pairing.mu.Unlock()
	return issued, nil
}

func (s *Server) pairingKeyValid(candidate string) bool {
	if s == nil || s.pairing == nil || candidate == "" {
		return false
	}
	now := s.pairing.now()
	s.pairing.mu.Lock()
	defer s.pairing.mu.Unlock()
	live := s.pairing.keys[:0]
	matched := false
	for _, item := range s.pairing.keys {
		if !item.ExpiresAt.After(now) {
			continue
		}
		live = append(live, item)
		if subtle.ConstantTimeCompare([]byte(item.Key), []byte(candidate)) == 1 {
			matched = true
		}
	}
	s.pairing.keys = live
	return matched
}

// PairingPayload is the JSON encoded inside the QR code.
type PairingPayload struct {
	V         int    `json:"v"`
	URL       string `json:"url"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	TTLHours  int    `json:"ttl_hours"`
	Name      string `json:"name,omitempty"`
}

func (k PairingKey) Payload() PairingPayload {
	hours := int(time.Until(k.ExpiresAt).Round(time.Hour) / time.Hour)
	if hours < 1 {
		hours = 1
	}
	name := k.Label
	if name == "" {
		name = "LuckyAgent"
	}
	return PairingPayload{
		V:         1,
		URL:       k.URL,
		Token:     k.Key,
		ExpiresAt: k.ExpiresAt.UTC().Format(time.RFC3339),
		TTLHours:  hours,
		Name:      name,
	}
}

func (k PairingKey) QRText() (string, error) {
	raw, err := json.Marshal(k.Payload())
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// LanAdvertiseURL picks one non-loopback IPv4 address for a wildcard bind.
// A concrete host in the listen address is kept as-is.
func LanAdvertiseURL(listenAddr string) (string, error) {
	return lanAdvertiseURL(listenAddr)
}

// lanAdvertiseURL picks one non-loopback IPv4 address for a wildcard bind.
// A concrete host in the listen address is kept as-is.
func lanAdvertiseURL(listenAddr string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		if strings.HasPrefix(listenAddr, ":") {
			host = ""
			port = strings.TrimPrefix(listenAddr, ":")
		} else {
			return "", fmt.Errorf("invalid listen address %q", listenAddr)
		}
	}
	if port == "" {
		return "", fmt.Errorf("listen address %q has no port", listenAddr)
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		ip, err := firstLANIPv4()
		if err != nil {
			return "", err
		}
		host = ip
	case "localhost":
		host = "127.0.0.1"
	}
	host = strings.Trim(host, "[]")
	return "http://" + net.JoinHostPort(host, port), nil
}

func firstLANIPv4() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := ipFromAddr(addr)
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			return ip.String(), nil
		}
	}
	return "", errors.New("no LAN IPv4 address found; pass --url")
}

func ipFromAddr(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}
