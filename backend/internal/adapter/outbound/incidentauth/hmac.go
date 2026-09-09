package incidentauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

const (
	AlgorithmHMACSHA256V1 = audit.IncidentBundleAlgorithmHMACSHA256V1
	minSecretBytes        = sha256.Size
)

var (
	ErrInvalidHMACConfig = errors.New("invalid incident HMAC configuration")

	_ tournamentadmin.IncidentAuthenticator = (*HMACAuthenticator)(nil)
)

type HMACConfig struct {
	KeyID  string
	Secret []byte
}

type HMACAuthenticator struct {
	keyID  string
	secret []byte
}

func NewHMACAuthenticator(cfg HMACConfig) (*HMACAuthenticator, error) {
	if !validHMACConfig(cfg) {
		return nil, ErrInvalidHMACConfig
	}
	return &HMACAuthenticator{keyID: cfg.KeyID, secret: append([]byte(nil), cfg.Secret...)}, nil
}

func (authenticator *HMACAuthenticator) Sign(bundle audit.IncidentBundle) (audit.IncidentBundle, error) {
	if !authenticator.valid() || audit.VerifyIncidentBundle(bundle) != nil {
		return audit.IncidentBundle{}, audit.ErrInvalidIncidentBundle
	}
	bundle.Algorithm = AlgorithmHMACSHA256V1
	bundle.KeyID = authenticator.keyID
	bundle.MAC = incidentMAC(authenticator.secret, bundle)
	return bundle, nil
}

func (authenticator *HMACAuthenticator) Verify(bundle audit.IncidentBundle) error {
	if !authenticator.valid() || audit.VerifyIncidentBundle(bundle) != nil ||
		audit.VerifyIncidentBundleAuthenticityEnvelope(bundle) != nil ||
		bundle.Algorithm != AlgorithmHMACSHA256V1 || bundle.KeyID != authenticator.keyID ||
		bundle.MAC == ([sha256.Size]byte{}) {
		return audit.ErrInvalidIncidentBundle
	}
	expected := incidentMAC(authenticator.secret, bundle)
	if !hmac.Equal(bundle.MAC[:], expected[:]) {
		return audit.ErrInvalidIncidentBundle
	}
	return nil
}

func (authenticator *HMACAuthenticator) valid() bool {
	return authenticator != nil && validHMACConfig(HMACConfig{
		KeyID: authenticator.keyID, Secret: authenticator.secret,
	})
}

func validHMACConfig(cfg HMACConfig) bool {
	return len(cfg.Secret) >= minSecretBytes && validHMACKeyID(cfg.KeyID)
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validHMACKeyID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index := range len(value) {
		current := value[index]
		letter := (current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z')
		digit := current >= '0' && current <= '9'
		if (index == 0 && !letter && !digit) ||
			(index > 0 && !letter && !digit && current != '.' && current != '_' && current != '-') {
			return false
		}
	}
	return true
}

func incidentMAC(secret []byte, bundle audit.IncidentBundle) [sha256.Size]byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(bundle.Algorithm))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(bundle.KeyID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(bundle.CanonicalContent)

	var result [sha256.Size]byte
	copy(result[:], mac.Sum(nil))
	return result
}
