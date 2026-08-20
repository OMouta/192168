//go:build windows

// Package tun is the virtual network adapter.
//
// Windows sends packets for the group's subnet to this adapter, the daemon
// encrypts them and puts them on the real network, and the reverse happens at
// the other end. A game sees an ordinary network interface and never knows.
package tun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// ringCapacity is the adapter's packet ring. Wintun wants a power of two, and
// 2 MiB is what WireGuard uses for a normal tunnel.
const ringCapacity = 0x200000

// adapterType is what Windows shows in network settings.
const adapterType = "192168"

// ErrNeedsAdmin means the adapter could not be created for lack of rights.
var ErrNeedsAdmin = errors.New("tun: creating the network adapter needs administrator rights")

// ErrMissingDriver means wintun.dll is not next to the daemon.
var ErrMissingDriver = errors.New("tun: wintun.dll is missing")

// ErrClosed means the adapter is going away, or already has.
var ErrClosed = errors.New("tun: adapter closed")

// Remove deletes the adapter if there is one.
//
// Uninstalling has to take the adapter with it, and a daemon that was killed
// rather than stopped leaves one behind.
func Remove(name string, log *slog.Logger) error {
	adapter, err := wintun.OpenAdapter(name)
	if err != nil {
		// Nothing to remove.
		return nil
	}
	if err := adapter.Close(); err != nil {
		return fmt.Errorf("tun: remove adapter %q: %w", name, err)
	}
	log.Info("adapter removed", "name", name)
	return nil
}

// Device is an open adapter.
type Device struct {
	log     *slog.Logger
	adapter *wintun.Adapter
	session wintun.Session

	// mu is held for reading by anything inside the driver and for writing by
	// Close, which is what stops a teardown racing a call in flight.
	mu        sync.RWMutex
	closing   atomic.Bool
	closeOnce sync.Once
}

// Open creates the adapter, gives it an address, and routes the group's subnet
// to it.
//
// Creating an adapter needs administrator rights, and the driver has to be
// beside the daemon. Both failures are told apart from each other, because one
// is fixed by the installer and the other by the user.
func Open(name string, address netip.Prefix, mtu int, log *slog.Logger) (*Device, error) {
	// An adapter left behind by a daemon that was killed would otherwise keep
	// the name and the old address.
	if existing, err := wintun.OpenAdapter(name); err == nil {
		log.Info("removing an adapter left over from a previous run", "name", name)
		existing.Close()
	}

	adapter, err := wintun.CreateAdapter(name, adapterType, nil)
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return nil, ErrNeedsAdmin
		case errors.Is(err, windows.ERROR_MOD_NOT_FOUND), errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
			return nil, ErrMissingDriver
		default:
			return nil, fmt.Errorf("tun: create adapter: %w", err)
		}
	}

	device := &Device{log: log, adapter: adapter}

	luid := winipcfg.LUID(adapter.LUID())
	if err := luid.SetIPAddresses([]netip.Prefix{address}); err != nil {
		device.Close()
		return nil, fmt.Errorf("tun: assign %s: %w", address, err)
	}

	// Assigning an address with a prefix gives Windows the on-link route for
	// the whole subnet, which is what sends a peer's traffic here.

	// The MTU has to leave room for the packet envelope, the AEAD tag, and the
	// UDP and IP headers underneath. Getting it wrong costs fragmentation
	// rather than connectivity, so a failure here is worth saying and not
	// worth giving up over.
	if iface, err := luid.IPInterface(winipcfg.AddressFamily(windows.AF_INET)); err != nil {
		log.Warn("cannot read the adapter interface", "error", err)
	} else {
		iface.NLMTU = uint32(mtu)
		if err := iface.Set(); err != nil {
			log.Warn("cannot set the adapter MTU", "mtu", mtu, "error", err)
		}
	}

	session, err := adapter.StartSession(ringCapacity)
	if err != nil {
		device.Close()
		return nil, fmt.Errorf("tun: start session: %w", err)
	}
	device.session = session

	log.Info("adapter up", "name", name, "address", address.String(), "mtu", mtu)
	return device, nil
}

// Read returns the next packet Windows wants sent. It blocks until there is
// one, ctx is cancelled, or the adapter goes away.
//
// The read lock is held for the whole call. Closing takes the write lock, so an
// adapter cannot be torn down while a read is inside the driver. Without that,
// disconnecting mid-read faults the process: the session is freed and
// ReceivePacket walks into it.
func (d *Device) Read(ctx context.Context) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for {
		if d.closing.Load() {
			return nil, ErrClosed
		}

		packet, err := d.session.ReceivePacket()
		switch {
		case err == nil:
			// The packet points into the adapter's ring, which is reused, so
			// the caller gets a copy.
			out := make([]byte, len(packet))
			copy(out, packet)
			d.session.ReleaseReceivePacket(packet)
			return out, nil

		case errors.Is(err, windows.ERROR_NO_MORE_ITEMS):
			if err := d.wait(ctx); err != nil {
				return nil, err
			}

		case errors.Is(err, windows.ERROR_HANDLE_EOF):
			return nil, ErrClosed

		default:
			return nil, fmt.Errorf("tun: read: %w", err)
		}
	}
}

// wait blocks until the adapter has something or the caller gives up.
func (d *Device) wait(ctx context.Context) error {
	// A short timeout rather than an infinite wait, so cancelling does not
	// depend on a packet arriving to be noticed.
	const slice = 250

	for {
		if d.closing.Load() {
			return ErrClosed
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// Waiting in slices rather than forever is what bounds how long a
		// close waits for this reader to let go of the lock.
		result, err := windows.WaitForSingleObject(d.session.ReadWaitEvent(), slice)
		if err != nil {
			return fmt.Errorf("tun: wait: %w", err)
		}
		if result == windows.WAIT_OBJECT_0 {
			return nil
		}
	}
}

// Write hands a decrypted packet to Windows as if it arrived from the network.
func (d *Device) Write(packet []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closing.Load() {
		return ErrClosed
	}

	out, err := d.session.AllocateSendPacket(len(packet))
	if err != nil {
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			// The ring is full because Windows is not draining it. Dropping is
			// what a real link does, and a game recovers from it.
			return nil
		}
		return fmt.Errorf("tun: allocate: %w", err)
	}
	copy(out, packet)
	d.session.SendPacket(out)
	return nil
}

// Close ends the session and removes the adapter, which takes its address and
// routes with it.
//
// It waits for readers and writers to leave the driver first. Ending a session
// underneath a call that is inside it faults the process, and disconnecting
// while a game is running is exactly when that would happen.
func (d *Device) Close() error {
	d.closeOnce.Do(func() {
		// Set before taking the lock, so a reader that wakes from its wait
		// sees it and returns instead of going back into the driver.
		d.closing.Store(true)

		d.mu.Lock()
		defer d.mu.Unlock()

		if d.session != (wintun.Session{}) {
			d.session.End()
		}
		if d.adapter != nil {
			d.adapter.Close()
		}
		d.log.Info("adapter down")
	})
	return nil
}
