//go:build windows

package tun

import (
	"errors"
	"fmt"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// multicastRoute is what a game's announcement is decided by. Every interface
// carries this one route, all of them identical, and the lowest total metric
// takes the traffic.
var multicastRoute = netip.MustParsePrefix("224.0.0.0/4")

// internetRoute is the machine's way out. Whichever interface holds the best
// one is carrying real traffic, and is not something to move for a game.
var internetRoute = netip.MustParsePrefix("0.0.0.0/0")

// claim is one interface's hold on the multicast route.
type claim struct {
	luid winipcfg.LUID
	name string

	// route is the route's own metric and metric is the interface's. Windows
	// compares the two added together, but a new interface metric has to be
	// worked out against the route's share alone.
	route  uint32
	metric uint32

	// auto is whether Windows works the interface metric out itself, which has
	// to be turned off to set one and put back to undo it.
	auto bool

	// internet means this interface holds the machine's best default route.
	internet bool
}

func (c claim) total() uint32 { return c.route + c.metric }

// yielded is an interface whose metric this adapter raised, and what it was
// before, so it can be given back.
type yielded struct {
	luid   winipcfg.LUID
	name   string
	metric uint32
	auto   bool
}

// multicastClaims reads what every connected interface's hold on the multicast
// route is worth.
func multicastClaims() ([]claim, error) {
	interfaces, err := winipcfg.GetIPInterfaceTable(winipcfg.AddressFamily(windows.AF_INET))
	if err != nil {
		return nil, fmt.Errorf("tun: read the interface table: %w", err)
	}

	// An interface that is down holds nothing worth counting, and raising its
	// metric would only surprise whoever brings it back up.
	live := make(map[winipcfg.LUID]*winipcfg.MibIPInterfaceRow, len(interfaces))
	for i := range interfaces {
		if interfaces[i].Connected {
			live[interfaces[i].InterfaceLUID] = &interfaces[i]
		}
	}

	routes, err := winipcfg.GetIPForwardTable2(winipcfg.AddressFamily(windows.AF_INET))
	if err != nil {
		return nil, fmt.Errorf("tun: read the route table: %w", err)
	}

	// Two passes, because whether an interface is carrying the machine's
	// internet traffic decides whether it may be touched at all, and that is
	// only known once every default route has been seen.
	internet, best := winipcfg.LUID(0), ^uint32(0)
	for i := range routes {
		iface, up := live[routes[i].InterfaceLUID]
		if !up || routes[i].DestinationPrefix.Prefix() != internetRoute {
			continue
		}
		if total := routes[i].Metric + iface.Metric; total < best {
			internet, best = routes[i].InterfaceLUID, total
		}
	}

	var claims []claim
	for i := range routes {
		iface, up := live[routes[i].InterfaceLUID]
		if !up || routes[i].DestinationPrefix.Prefix() != multicastRoute {
			continue
		}
		claims = append(claims, claim{
			luid:     routes[i].InterfaceLUID,
			name:     interfaceName(routes[i].InterfaceLUID),
			route:    routes[i].Metric,
			metric:   iface.Metric,
			auto:     iface.UseAutomaticMetric,
			internet: routes[i].InterfaceLUID == internet,
		})
	}
	return claims, nil
}

// takeMulticastRoute moves whatever is beating this adapter for the multicast
// route out of the way, and names anything left holding it.
//
// Setting this adapter's own metric low is not enough on its own. Another
// virtual network pins its metric to the same floor for the same reason, and
// then the two tie: Windows hands the route to one of them without saying
// which, and does not revisit it. Measured against Radmin VPN on a tied metric,
// two hundred multicast addresses out of two hundred went to Radmin, so a tie
// has to be treated as a loss rather than a coin flip.
func (d *Device) takeMulticastRoute() (string, error) {
	claims, err := multicastClaims()
	if err != nil {
		return "", err
	}

	moves, held, err := contest(claims, d.luid)
	if err != nil {
		return "", err
	}

	d.metricMu.Lock()
	defer d.metricMu.Unlock()

	for _, m := range moves {
		if err := setInterfaceMetric(m.rival.luid, m.metric, false); err != nil {
			d.log.Warn("cannot move an adapter off the discovery route", "adapter", m.rival.name, "error", err)
			held = m.rival.name
			continue
		}

		d.log.Info("moved an adapter off the discovery route",
			"adapter", m.rival.name, "wasMetric", m.rival.metric, "nowMetric", m.metric)
		d.yielded = append(d.yielded, yielded{
			luid:   m.rival.luid,
			name:   m.rival.name,
			metric: m.rival.metric,
			auto:   m.rival.auto,
		})
	}

	return held, nil
}

// move is one interface that has to give way, and the metric that does it.
type move struct {
	rival  claim
	metric uint32
}

// contest works out what has to move for ours to hold the multicast route, and
// names anything in the way that must be left alone.
func contest(claims []claim, ours winipcfg.LUID) ([]move, string, error) {
	var mine *claim
	for i := range claims {
		if claims[i].luid == ours {
			mine = &claims[i]
		}
	}
	if mine == nil {
		// Windows adds this route along with the address, so its absence is
		// the adapter not being finished rather than a rival having won.
		return nil, "", errors.New("tun: the adapter has no multicast route")
	}

	var moves []move
	held := ""
	for _, rival := range claims {
		// A tie counts as a loss, which is the whole point: two adapters on
		// the same metric is exactly the case this exists for.
		if rival.luid == ours || rival.total() > mine.total() {
			continue
		}

		if rival.internet {
			// Raising this one could hand the machine's internet traffic to a
			// different interface. An empty LAN list is worth telling somebody
			// about. Taking their connection away to fix it is not.
			held = rival.name
			continue
		}

		// One past ours, which is all it takes and is the least that can be
		// taken from whatever else that adapter is for. The subtraction cannot
		// wrap: a rival that got this far has a total no larger than ours, and
		// its route metric is part of that total.
		moves = append(moves, move{rival: rival, metric: mine.total() - rival.route + 1})
	}

	return moves, held, nil
}

// yieldBack gives every adapter this one demoted its metric back.
//
// Turning the switch off is somebody asking for their printers and speakers
// again, and so is disconnecting. Leaving another adapter demoted would only
// half give them back.
func (d *Device) yieldBack() {
	d.metricMu.Lock()
	defer d.metricMu.Unlock()

	for _, was := range d.yielded {
		if err := setInterfaceMetric(was.luid, was.metric, was.auto); err != nil {
			d.log.Warn("cannot give an adapter its metric back", "adapter", was.name, "error", err)
			continue
		}
		d.log.Info("gave an adapter the discovery route back", "adapter", was.name)
	}
	d.yielded = nil
}

// setInterfaceMetric pins an interface's metric, or hands it back to Windows.
func setInterfaceMetric(luid winipcfg.LUID, metric uint32, automatic bool) error {
	iface, err := luid.IPInterface(winipcfg.AddressFamily(windows.AF_INET))
	if err != nil {
		return fmt.Errorf("tun: read the interface: %w", err)
	}

	iface.UseAutomaticMetric = automatic
	iface.Metric = metric
	if err := iface.Set(); err != nil {
		return fmt.Errorf("tun: set the interface metric: %w", err)
	}
	return nil
}

// interfaceName is what the adapter is called in network settings, which is the
// only name for it a user has ever seen.
func interfaceName(luid winipcfg.LUID) string {
	if info, err := luid.Interface(); err == nil {
		return info.Alias()
	}
	return "Another network adapter"
}
