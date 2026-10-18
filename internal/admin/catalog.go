package admin

import (
	"fmt"

	"github.com/moherik/openpay.id/pkg/provider"
)

// Field describes one provider configuration field as the console renders it.
// Secret fields are write-only: the API never echoes their stored value back.
type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Secret      bool     `json:"secret,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Multiline   bool     `json:"multiline,omitempty"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
}

// Adapter is the catalog entry for one registered provider adapter. It carries
// the human-facing metadata the console's providers page and routing editor
// need. Runtime truth (which adapters exist, exact capabilities) is validated
// against the live registry by a unit test, and unknown adapters still render
// via a capability probe.
type Adapter struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Summary string `json:"summary"`
	// Environments lists which environments the adapter makes sense in.
	Environments []string `json:"environments"`
	// Methods/Inquiry/Cancel mirror the adapter's Capabilities().Static copies
	// here because no instance may exist yet.
	Methods []string `json:"methods"`
	// Fields are the configuration fields the console's credentials dialog
	// renders.
	Fields []Field `json:"config_fields"`
}

// adapterCatalog is keyed by adapter name (the factory registration name).
var adapterCatalog = map[string]Adapter{
	"mock": {
		Name:  "mock",
		Label: "Mock gateway",
		Summary: "In-memory gateway used for local development and tests. " +
			"Issues virtual accounts instantly and fires callbacks on demand.",
		Environments: []string{"sandbox"},
		Methods:      []string{"virtual_account", "qris", "ewallet", "retail_outlet"},
		Fields: []Field{
			{Key: "secret", Label: "Callback secret", Secret: true, Placeholder: "mock-secret", Help: "HMAC secret used to sign mock callbacks."},
			{Key: "va_prefix", Label: "VA number prefix", Placeholder: "88888", Help: "Virtual account number prefix."},
		},
	},
	"xendit": {
		Name:  "xendit",
		Label: "Xendit",
		Summary: "Xendit APIs. Virtual accounts, cards and e-wallets with " +
			"sandbox support via secret_key.",
		Environments: []string{"sandbox", "production"},
		Methods:      []string{"virtual_account", "credit_card", "ewallet", "qris", "retail_outlet"},
		Fields: []Field{
			{Key: "secret_key", Label: "Secret key", Secret: true, Required: true, Help: "Xendit secret key (sk_...)."},
			{Key: "callback_token", Label: "Callback verification token", Secret: true, Help: "Xendit callback verification token."},
			{Key: "return_url", Label: "Payer return URL", Help: "Deep-link return URL for e-wallet redirects."},
			{Key: "base_url", Label: "API base URL", Placeholder: "https://api.xendit.co", Help: "Override only for testing."},
		},
	},
	"midtrans": {
		Name:  "midtrans",
		Label: "Midtrans (Core API)",
		Summary: "Midtrans Core API. Charges via server_key with direct " +
			"VA / e-wallet / QRIS endpoints.",
		Environments: []string{"sandbox", "production"},
		Methods:      []string{"virtual_account", "qris", "ewallet", "retail_outlet"},
		Fields: []Field{
			{Key: "server_key", Label: "Server key", Secret: true, Required: true, Help: "Midtrans server key."},
			{Key: "qris_acquirer", Label: "QRIS acquirer", Options: []string{"gopay", "bank", "airpay"}, Help: "QRIS acquirer to request."},
			{Key: "gopay_callback_url", Label: "GoPay callback URL", Help: "Callback URL registered for GoPay."},
			{Key: "shopeepay_callback_url", Label: "ShopeePay callback URL", Help: "Callback URL registered for ShopeePay."},
			{Key: "base_url", Label: "API base URL", Placeholder: "https://api.sandbox.midtrans.com", Help: "Override only for testing."},
		},
	},
	"midtrans-snap": {
		Name:  "midtrans-snap",
		Label: "Midtrans Snap",
		Summary: "Midtrans Snap v2 API with OAuth client credentials and QRIS " +
			"(mutual TLS style) access tokens.",
		Environments: []string{"sandbox", "production"},
		Methods:      []string{"virtual_account", "qris", "ewallet"},
		Fields: []Field{
			{Key: "client_id", Label: "Client ID", Required: true, Help: "Midtrans OAuth client id (issuer)."},
			{Key: "client_secret", Label: "Client secret", Secret: true, Required: true, Help: "Midtrans OAuth client secret."},
			{Key: "private_key", Label: "Private key (PEM)", Secret: true, Required: true, Multiline: true, Help: "PEM private key used to sign access-token requests."},
			{Key: "partner_id", Label: "Partner ID", Help: "Midtrans partner id header."},
			{Key: "merchant_id", Label: "Merchant ID", Help: "Midtrans merchant id header."},
			{Key: "partner_service_id", Label: "VA service prefix (BIN)", Help: "Virtual account service prefix."},
			{Key: "channel_id", Label: "Channel ID", Placeholder: "DANA", Help: "Snap channel id (e.g. DANA)."},
			{Key: "device_id", Label: "Device ID", Help: "Snap device id header."},
			{Key: "base_url", Label: "API base URL", Placeholder: "https://api.sandbox.midtrans.com", Help: "Override only for testing."},
		},
	},
	"doku": {
		Name:  "doku",
		Label: "DOKU",
		Summary: "DOKU Snap API. Virtual accounts, QRIS and e-wallets with " +
			"client-credential access tokens.",
		Environments: []string{"sandbox", "production"},
		Methods:      []string{"virtual_account", "qris", "ewallet"},
		Fields: []Field{
			{Key: "client_id", Label: "Client ID", Required: true, Help: "DOKU client id (client-Id)."},
			{Key: "client_secret", Label: "Client secret", Secret: true, Required: true, Help: "DOKU client secret."},
			{Key: "private_key", Label: "Private key (PEM)", Secret: true, Required: true, Multiline: true, Help: "PEM private key used to sign token requests."},
			{Key: "partner_service_id", Label: "VA service prefix (BIN)", Help: "Virtual account service prefix."},
			{Key: "merchant_id", Label: "Merchant ID", Help: "DOKU merchant id."},
			{Key: "terminal_id", Label: "Terminal ID", Help: "DOKU terminal id."},
			{Key: "base_url", Label: "API base URL", Placeholder: "https://api-sandbox.doku.com", Help: "Override only for testing."},
		},
	},
	"winpay": {
		Name:  "winpay",
		Label: "Winpay",
		Summary: "Winpay QRIS and virtual accounts (partner credential " +
			"signature scheme).",
		Environments: []string{"sandbox", "production"},
		Methods:      []string{"qris", "virtual_account"},
		Fields: []Field{
			{Key: "partner_id", Label: "Partner ID", Required: true, Help: "Winpay partner id."},
			{Key: "private_key", Label: "Private key (PEM)", Secret: true, Required: true, Multiline: true, Help: "PEM private key used to sign requests."},
			{Key: "channel_id", Label: "Channel ID", Help: "Winpay channel id."},
			{Key: "base_url", Label: "API base URL", Placeholder: "https://winpay.dev", Help: "Override only for testing."},
		},
	},
}

// adaptersFor returns catalog entries in stable order,.Boolean fill gaps for
// factories registered in the binary but missing from the static catalog (a
// provider package added without touching the console). Metadata for those is
// probed at runtime; their config fields come back empty rather than wrong.
func (s *Server) adaptersFor() []Adapter {
	registered := map[string]bool{}
	for _, name := range s.registry.FactoryNames() {
		registered[name] = true
	}

	out := make([]Adapter, 0, len(adapterCatalog)+2)
	for _, name := range orderedAdapters {
		a, ok := adapterCatalog[name]
		if !ok || !registered[name] {
			continue // compiled out: not shipped in this binary.
		}
		out = append(out, a)
	}

	for _, name := range s.registry.FactoryNames() {
		if _, ok := adapterCatalog[name]; ok {
			continue
		}
		a := Adapter{Name: name, Label: name}
		if caps, err := probeCapabilities(s.registry, name); err == nil {
			a.Methods = methodNames(caps.Methods)
			a.Environments = probeEnvironments(caps)
		}
		out = append(out, a)
	}

	return out
}

// orderedAdapters fixes the display order of the known adapters.
var orderedAdapters = []string{"mock", "xendit", "midtrans", "midtrans-snap", "doku", "winpay"}

// probeCapabilities builds a throwaway instance using the catalog's required
// fields ("probe" values) to read Capabilities() without network traffic.
func probeCapabilities(r *registryView, adapter string) (provider.Capabilities, error) {
	var cfg provider.Config
	if a, ok := adapterCatalog[adapter]; ok {
		for _, f := range a.Fields {
			if f.Required {
				cfg[f.Key] = "probe"
			}
		}
	}
	p, err := r.BuildAs("(probe)"+adapter, adapter, cfg)
	if err != nil {
		return provider.Capabilities{}, fmt.Errorf("admin: probing %q: %w", adapter, err)
	}
	r.Remove("(probe)" + adapter)
	return p.Capabilities(), nil
}

// registryView is the slice of *provider.Registry the admin handlers use.
type registryView = provider.Registry

func methodNames(ms []provider.Method) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, string(m))
	}
	return out
}

func probeEnvironments(caps provider.Capabilities) []string {
	if caps.SupportsSandbox {
		return []string{"sandbox", "production"}
	}
	return []string{"sandbox"}
}
