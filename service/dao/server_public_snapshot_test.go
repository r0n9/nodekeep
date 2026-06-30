package dao

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r0n9/nodekeep/model"
)

func TestSortedPublicServerSnapshotRedactsSensitiveFields(t *testing.T) {
	InitServerRuntimeState()
	UpsertServerRuntime(model.Server{
		Common:       model.Common{ID: 1},
		Name:         "public-node",
		Tag:          "edge",
		Secret:       "secret-token",
		Note:         "private-note",
		DisplayIndex: 10,
	}, false)
	UpdateServerHost(1, model.Host{
		Platform:        "linux",
		PlatformVersion: "6.8",
		CPU:             []string{"secret-cpu"},
		MemTotal:        1024,
		DiskTotal:       2048,
		SwapTotal:       512,
		Arch:            "amd64",
		Virtualization:  "kvm",
		BootTime:        123,
		IP:              "203.0.113.10",
		CountryCode:     "US",
		Version:         "1.0.0",
	}, false)
	UpdateServerState(1, model.HostState{CPU: 12.5, Uptime: 99}, time.Unix(100, 0))

	servers := SortedPublicServerSnapshot()
	if len(servers) != 1 {
		t.Fatalf("public snapshot length = %d, want 1", len(servers))
	}
	if servers[0].Host == nil {
		t.Fatal("public snapshot host is nil")
	}

	data, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("marshal public snapshot: %v", err)
	}
	payload := string(data)
	for _, sensitive := range []string{"203.0.113.10", "secret-token", "private-note", "1.0.0", `"IP"`, `"Secret"`, `"Note"`, `"Version"`} {
		if strings.Contains(payload, sensitive) {
			t.Fatalf("public snapshot JSON leaked %q: %s", sensitive, payload)
		}
	}
	for _, expected := range []string{"public-node", "edge", "linux", "amd64"} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("public snapshot JSON missing %q: %s", expected, payload)
		}
	}
}

func TestSortedPublicServerSnapshotIsDeepCopy(t *testing.T) {
	InitServerRuntimeState()
	UpsertServerRuntime(model.Server{
		Common: model.Common{ID: 1},
		Name:   "node",
	}, false)
	UpdateServerHost(1, model.Host{CPU: []string{"cpu-a"}}, false)
	UpdateServerState(1, model.HostState{Uptime: 1}, time.Unix(100, 0))

	servers := SortedPublicServerSnapshot()
	servers[0].Name = "changed"
	servers[0].Host.CPU[0] = "changed-cpu"
	servers[0].State.Uptime = 999

	fresh := SortedPublicServerSnapshot()
	if fresh[0].Name != "node" {
		t.Fatalf("public snapshot mutation changed runtime name: %q", fresh[0].Name)
	}
	if fresh[0].Host.CPU[0] != "cpu-a" {
		t.Fatalf("public snapshot mutation changed runtime CPU: %q", fresh[0].Host.CPU[0])
	}
	if fresh[0].State.Uptime != 1 {
		t.Fatalf("public snapshot mutation changed runtime state uptime: %d", fresh[0].State.Uptime)
	}
}

func TestSortedServerSnapshotOrdersByGroupDisplayIndexAndID(t *testing.T) {
	InitServerRuntimeState()
	for _, server := range []model.Server{
		{Common: model.Common{ID: 1}, Name: "beta-high", Tag: "beta", DisplayIndex: 100},
		{Common: model.Common{ID: 2}, Name: "alpha-low", Tag: "alpha", DisplayIndex: 20},
		{Common: model.Common{ID: 3}, Name: "alpha-high-a", Tag: "alpha", DisplayIndex: 50},
		{Common: model.Common{ID: 4}, Name: "alpha-high-b", Tag: "alpha", DisplayIndex: 50},
	} {
		UpsertServerRuntime(server, false)
	}

	servers := SortedServerSnapshot()
	got := make([]uint64, 0, len(servers))
	for _, server := range servers {
		got = append(got, server.ID)
	}
	want := []uint64{1, 4, 3, 2}
	if len(got) != len(want) {
		t.Fatalf("sorted server count = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted server ids = %v, want %v", got, want)
		}
	}
}

func TestUpdateServerHostKeepsAgentCountryCodeWhenDashboardGeoIPUnavailable(t *testing.T) {
	InitServerRuntimeState()
	UpsertServerRuntime(model.Server{
		Common: model.Common{ID: 1},
		Name:   "node",
	}, false)

	UpdateServerHost(1, model.Host{
		IP:          "IPs[IPv4:203.0.113.10,IPv6:]",
		CountryCode: "US",
	}, false)

	servers := SortedPublicServerSnapshot()
	if len(servers) != 1 || servers[0].Host == nil {
		t.Fatalf("server host missing after update: %#v", servers)
	}
	if servers[0].Host.CountryCode != "us" {
		t.Fatalf("country code = %q, want us", servers[0].Host.CountryCode)
	}
}
