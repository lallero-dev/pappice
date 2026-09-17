package app

import "testing"

func TestConfigTLSEnabled(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantTLS bool
		wantErr bool
	}{
		{"plain http", Config{}, false, false},
		{"tls", Config{TLSCert: "cert.pem", TLSKey: "key.pem"}, true, false},
		{"missing key", Config{TLSCert: "cert.pem"}, false, true},
		{"missing cert", Config{TLSKey: "key.pem"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.TLSEnabled()
			if (err != nil) != tt.wantErr {
				t.Fatalf("TLSEnabled error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantTLS {
				t.Fatalf("TLSEnabled() = %v, want %v", got, tt.wantTLS)
			}
		})
	}
}
