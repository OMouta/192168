package core

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
)

// minecraftPing builds the datagram a Minecraft world announces itself with:
// UDP to 224.0.2.60:4445, from a peer's virtual address.
func minecraftPing(destination netip.Addr, body string) []byte {
	packet := make([]byte, 20+udpHeaderSize+len(body))

	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	packet[8] = 1 // multicast discovery goes out with a time to live of one
	packet[9] = protocolUDP
	copy(packet[12:16], netip.MustParseAddr("10.69.0.1").AsSlice())
	copy(packet[16:20], destination.AsSlice())
	binary.BigEndian.PutUint16(packet[10:12], ones(packet[:20]))

	udp := packet[20:]
	binary.BigEndian.PutUint16(udp[0:2], 61234)
	binary.BigEndian.PutUint16(udp[2:4], 4445)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpHeaderSize+len(body)))
	copy(udp[udpHeaderSize:], body)
	binary.BigEndian.PutUint16(udp[6:8], udpChecksum(packet))

	return packet
}

// ones is the one's complement sum every IP checksum is, computed the slow way
// so the incremental version has something independent to be checked against.
func ones(data []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(data); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for sum > 0xffff {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// udpChecksum computes one over the pseudo header and the datagram, with the
// checksum field itself zeroed.
func udpChecksum(packet []byte) uint16 {
	udp := packet[20:]

	pseudo := make([]byte, 0, 12+len(udp))
	pseudo = append(pseudo, packet[12:20]...) // source and destination
	pseudo = append(pseudo, 0, protocolUDP)
	pseudo = binary.BigEndian.AppendUint16(pseudo, uint16(len(udp)))

	scratch := append(pseudo, udp...)
	binary.BigEndian.PutUint16(scratch[12+6:12+8], 0)
	return ones(scratch)
}

func TestLocaliseMulticastReaddressesAndKeepsChecksumsValid(t *testing.T) {
	local := netip.MustParseAddr("10.69.0.2")
	packet := minecraftPing(netip.MustParseAddr("224.0.2.60"), "[MOTD]A world[/MOTD][AD]25565[/AD]")

	localiseMulticast(packet, local)

	if got := netip.AddrFrom4([4]byte(packet[16:20])); got != local {
		t.Fatalf("destination is %s, want %s", got, local)
	}
	// A game reads the source to work out who is hosting, so it has to survive.
	if got := netip.AddrFrom4([4]byte(packet[12:16])); got.String() != "10.69.0.1" {
		t.Fatalf("source is %s, want 10.69.0.1", got)
	}

	// The incremental correction has to land on what a full recomputation
	// would have produced, or Windows drops the packet before the game sees it.
	if got, want := binary.BigEndian.Uint16(packet[10:12]), recomputeHeader(packet); got != want {
		t.Errorf("header checksum is %#04x, want %#04x", got, want)
	}
	if got, want := binary.BigEndian.Uint16(packet[26:28]), udpChecksum(packet); got != want {
		t.Errorf("udp checksum is %#04x, want %#04x", got, want)
	}
}

func recomputeHeader(packet []byte) uint16 {
	header := bytes.Clone(packet[:20])
	binary.BigEndian.PutUint16(header[10:12], 0)
	return ones(header)
}

// A datagram sent without a checksum has to stay that way. Correcting the zero
// would produce a checksum that is wrong for the bytes on the wire.
func TestLocaliseMulticastLeavesAnAbsentChecksumAlone(t *testing.T) {
	packet := minecraftPing(netip.MustParseAddr("224.0.2.60"), "no checksum")
	binary.BigEndian.PutUint16(packet[26:28], 0)

	localiseMulticast(packet, netip.MustParseAddr("10.69.0.2"))

	if got := binary.BigEndian.Uint16(packet[26:28]); got != 0 {
		t.Errorf("checksum is %#04x, want it left at zero", got)
	}
}

func TestLocaliseMulticastLeavesEverythingElseAlone(t *testing.T) {
	local := netip.MustParseAddr("10.69.0.2")

	unicast := minecraftPing(netip.MustParseAddr("10.69.0.2"), "game traffic")
	broadcast := minecraftPing(netip.MustParseAddr("10.69.0.255"), "older discovery")

	// A broadcast needs no membership to be delivered, so it already arrives
	// and readdressing it would only hide who it was meant for.
	for name, packet := range map[string][]byte{"unicast": unicast, "broadcast": broadcast} {
		before := bytes.Clone(packet)
		localiseMulticast(packet, local)
		if !bytes.Equal(packet, before) {
			t.Errorf("%s packet was rewritten", name)
		}
	}

	// TCP to a group address is not something to be readdressing, and there is
	// no checksum here this knows how to correct.
	tcp := minecraftPing(netip.MustParseAddr("224.0.2.60"), "not udp")
	tcp[9] = 6
	before := bytes.Clone(tcp)
	localiseMulticast(tcp, local)
	if !bytes.Equal(tcp, before) {
		t.Error("a non-UDP packet was rewritten")
	}

	// Half a datagram carries no transport header, and moving the address on
	// one fragment would leave the whole thing unreassemblable.
	fragment := minecraftPing(netip.MustParseAddr("224.0.2.60"), "part of something")
	binary.BigEndian.PutUint16(fragment[6:8], 0x2000) // more fragments
	before = bytes.Clone(fragment)
	localiseMulticast(fragment, local)
	if !bytes.Equal(fragment, before) {
		t.Error("a fragment was rewritten")
	}
}

func TestLocaliseMulticastSurvivesRunts(t *testing.T) {
	local := netip.MustParseAddr("10.69.0.2")

	// Truncated, header-only, and a header claiming options that are not there.
	truncated := minecraftPing(netip.MustParseAddr("224.0.2.60"), "cut short")[:24]
	options := minecraftPing(netip.MustParseAddr("224.0.2.60"), "lying about options")
	options[0] = 0x4f // fifteen words of header, more than the packet holds

	for _, packet := range [][]byte{nil, {}, {0x45}, truncated, options} {
		localiseMulticast(packet, local)
	}
}
