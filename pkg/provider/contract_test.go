package provider_test

import (
	"testing"

	"github.com/moherik/openpay.id/pkg/provider"
)

func TestCapabilitiesSupportsMethod(t *testing.T) {
	specified := provider.Capabilities{Methods: []provider.Method{provider.MethodVirtualAccount, provider.MethodQRIS}}
	if !specified.SupportsMethod(provider.MethodQRIS) {
		t.Error("expected qris to be supported")
	}
	if specified.SupportsMethod(provider.MethodEWallet) {
		t.Error("expected ewallet to be unsupported")
	}

	unspecified := provider.Capabilities{}
	if !unspecified.SupportsMethod(provider.MethodCard) {
		t.Error("an empty Methods slice means \"supports everything\"")
	}
}

func TestCapabilitiesSupportsChannel(t *testing.T) {
	caps := provider.Capabilities{Channels: []string{"BCA", "QRIS"}}

	for _, channel := range []string{"BCA", "bca", "  Bca  ", ""} {
		if !caps.SupportsChannel(channel) {
			t.Errorf("expected channel %q to be accepted", channel)
		}
	}
	if caps.SupportsChannel("OVO") {
		t.Error("expected an unadvertised channel to be rejected")
	}

	unspecified := provider.Capabilities{}
	if !unspecified.SupportsChannel("anything") {
		t.Error("an empty Channels slice means \"supports everything\"")
	}
}
