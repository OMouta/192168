package core

import (
	"net/netip"
	"testing"
)

func TestAdapterAddress(t *testing.T) {
	tests := []struct {
		name      string
		virtualIP string
		subnet    string
		want      string
		wantErr   bool
	}{
		// The prefix length has to come from the group, not the address, or
		// Windows routes one host here instead of the whole subnet and no peer
		// is reachable.
		{
			name:      "address takes the group's prefix",
			virtualIP: "10.69.0.3",
			subnet:    "10.69.0.0/24",
			want:      "10.69.0.3/24",
		},
		{
			name:      "outside the subnet",
			virtualIP: "192.168.1.5",
			subnet:    "10.69.0.0/24",
			wantErr:   true,
		},
		{
			name:      "unusable address",
			virtualIP: "not-an-address",
			subnet:    "10.69.0.0/24",
			wantErr:   true,
		},
		{
			name:      "unusable subnet",
			virtualIP: "10.69.0.3",
			subnet:    "10.69.0.0",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := adapterAddress(tt.virtualIP, tt.subnet)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("got %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("adapterAddress: %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("address = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDestinationOf(t *testing.T) {
	// A minimal IPv4 header. Only the version nibble and the destination are
	// read, so the rest is left as zeroes.
	packet := make([]byte, 20)
	packet[0] = 0x45
	copy(packet[16:20], []byte{10, 69, 0, 2})

	got, ok := destinationOf(packet)
	if !ok {
		t.Fatal("a valid IPv4 packet was rejected")
	}
	if got != netip.MustParseAddr("10.69.0.2") {
		t.Errorf("destination = %v, want 10.69.0.2", got)
	}

	// The overlay is IPv4 only. Anything else has nowhere to go, and reading
	// bytes 16 to 20 of an IPv6 header would produce a plausible looking
	// address that belongs to nobody.
	ipv6 := make([]byte, 40)
	ipv6[0] = 0x60
	if _, ok := destinationOf(ipv6); ok {
		t.Error("an IPv6 packet was treated as IPv4")
	}

	for _, runt := range [][]byte{nil, make([]byte, 19)} {
		if _, ok := destinationOf(runt); ok {
			t.Errorf("a %d byte packet was accepted", len(runt))
		}
	}
}
