// Package lanscan discovers other hosts on the same local network as the
// running machine, for the "Add tunnel" UI's local-host suggestions. It
// never needs elevated privileges: it nudges the OS into resolving ARP
// entries for nearby addresses with ordinary TCP dials, then reads whatever
// the kernel's ARP table already knows.
package lanscan

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Device is one host discovered on the local network.
type Device struct {
	IP       string
	MAC      string
	Hostname string // best-effort reverse DNS, "" if it didn't resolve
}

const (
	probeTimeout   = 250 * time.Millisecond
	probeWorkers   = 128
	dnsTimeout     = 500 * time.Millisecond
	maxSubnetHosts = 1022 // skip probing anything bigger than a /22
)

// Scan probes locally-attached IPv4 subnets to populate the OS ARP table,
// then reads it back plus best-effort hostnames. It respects ctx for the
// overall time budget (probing is skipped/cut short once ctx is done, but
// whatever ARP entries already exist are still returned).
func Scan(ctx context.Context) ([]Device, error) {
	ownIPs, subnets := localIPv4Subnets()

	var wg sync.WaitGroup
	sem := make(chan struct{}, probeWorkers)
	for _, subnet := range subnets {
		for _, ip := range hostsIn(subnet) {
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(ip net.IP) {
				defer wg.Done()
				defer func() { <-sem }()
				probe(ctx, ip)
			}(ip)
		}
	}
	wg.Wait()

	entries, err := readARPTable()
	if err != nil {
		return nil, err
	}

	out := make([]Device, 0, len(entries))
	for _, e := range entries {
		if ownIPs[e.IP] || !isUsableMAC(e.MAC) {
			continue
		}
		e.Hostname = reverseLookup(ctx, e.IP)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return ipLess(out[i].IP, out[j].IP) })
	return out, nil
}

// probe triggers OS-level ARP resolution for ip by attempting a short TCP
// dial. The dial's actual success/failure is irrelevant — a live host
// answers ARP as soon as the kernel tries to route a packet to it.
func probe(ctx context.Context, ip net.IP) {
	d := net.Dialer{Timeout: probeTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), "80"))
	if err == nil {
		conn.Close()
	}
}

// localIPv4Subnets returns this machine's own IPv4 addresses (to exclude
// from results) and the private IPv4 subnets it's directly attached to,
// skipping anything larger than maxSubnetHosts so a misconfigured huge
// network doesn't turn every scan into a multi-minute sweep.
func localIPv4Subnets() (own map[string]bool, subnets []*net.IPNet) {
	own = make(map[string]bool)
	ifaces, err := net.Interfaces()
	if err != nil {
		return own, nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !ip4.IsPrivate() {
				continue
			}
			own[ip4.String()] = true
			ones, bits := ipnet.Mask.Size()
			if bits != 32 || (1<<uint(32-ones))-2 > maxSubnetHosts {
				continue
			}
			subnets = append(subnets, &net.IPNet{IP: ip4.Mask(ipnet.Mask), Mask: ipnet.Mask})
		}
	}
	return own, subnets
}

// hostsIn enumerates every usable host address in subnet (excluding the
// network and broadcast addresses).
func hostsIn(subnet *net.IPNet) []net.IP {
	ones, bits := subnet.Mask.Size()
	count := 1 << uint(bits-ones)
	if count <= 2 {
		return nil
	}
	base := subnet.IP.To4()
	baseInt := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])

	out := make([]net.IP, 0, count-2)
	for i := 1; i < count-1; i++ {
		v := baseInt + uint32(i)
		out = append(out, net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)))
	}
	return out
}

func reverseLookup(ctx context.Context, ip string) string {
	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

func isUsableMAC(mac string) bool {
	return mac != "" && mac != "00:00:00:00:00:00" && !strings.Contains(mac, "incomplete")
}

func ipLess(a, b string) bool {
	ipa, ipb := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	if ipa == nil || ipb == nil {
		return a < b
	}
	for i := range ipa {
		if ipa[i] != ipb[i] {
			return ipa[i] < ipb[i]
		}
	}
	return false
}

func readARPTable() ([]Device, error) {
	if runtime.GOOS == "linux" {
		return readProcNetARP()
	}
	return readARPCommand()
}

// readProcNetARP parses Linux's /proc/net/arp, a whitespace-separated table:
// "IP address  HW type  Flags  HW address  Mask  Device".
func readProcNetARP() ([]Device, error) {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Device
	scanner := bufio.NewScanner(f)
	scanner.Scan() // header line
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		out = append(out, Device{IP: fields[0], MAC: fields[3]})
	}
	return out, scanner.Err()
}

// readARPCommand parses the output of `arp -an`, used on non-Linux
// platforms (macOS) that don't expose an ARP table as a plain file. A
// typical line looks like:
// "? (192.168.1.5) at aa:bb:cc:dd:ee:ff on en0 ifscope [ethernet]"
func readARPCommand() ([]Device, error) {
	out, err := exec.Command("arp", "-an").Output()
	if err != nil {
		return nil, err
	}
	var devices []Device
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		var ip, mac string
		for i, f := range fields {
			if strings.HasPrefix(f, "(") && strings.HasSuffix(f, ")") {
				ip = strings.Trim(f, "()")
			}
			if f == "at" && i+1 < len(fields) {
				mac = fields[i+1]
			}
		}
		if ip != "" && mac != "" {
			devices = append(devices, Device{IP: ip, MAC: mac})
		}
	}
	return devices, nil
}
