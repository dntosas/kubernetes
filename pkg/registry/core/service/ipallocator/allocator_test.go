/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ipallocator

import (
	"net"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/metrics/testutil"
	api "k8s.io/kubernetes/pkg/apis/core"
)

func TestAllocate(t *testing.T) {
	testCases := []struct {
		name             string
		cidr             string
		family           api.IPFamily
		free             int
		released         string
		outOfRange       []string
		alreadyAllocated string
	}{
		{
			name:     "IPv4",
			cidr:     "192.168.1.0/24",
			family:   api.IPv4Protocol,
			free:     254,
			released: "192.168.1.5",
			outOfRange: []string{
				"192.168.0.1",   // not in 192.168.1.0/24
				"192.168.1.0",   // reserved (base address)
				"192.168.1.255", // reserved (broadcast address)
				"192.168.2.2",   // not in 192.168.1.0/24
			},
			alreadyAllocated: "192.168.1.1",
		},
		{
			name:     "IPv6",
			cidr:     "2001:db8:1::/48",
			family:   api.IPv6Protocol,
			free:     65535,
			released: "2001:db8:1::5",
			outOfRange: []string{
				"2001:db8::1",     // not in 2001:db8:1::/48
				"2001:db8:1::",    // reserved (base address)
				"2001:db8:1::1:0", // not in the low 16 bits of 2001:db8:1::/48
				"2001:db8:2::2",   // not in 2001:db8:1::/48
			},
			alreadyAllocated: "2001:db8:1::1",
		},
	}
	for _, tc := range testCases {
		_, cidr, err := net.ParseCIDR(tc.cidr)
		if err != nil {
			t.Fatal(err)
		}
		r, err := NewInMemory(cidr)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("base: %v", r.base.Bytes())
		if f := r.Free(); f != tc.free {
			t.Errorf("Test %s unexpected free %d", tc.name, f)
		}

		rCIDR := r.CIDR()
		if rCIDR.String() != tc.cidr {
			t.Errorf("allocator returned a different cidr")
		}

		if r.IPFamily() != tc.family {
			t.Errorf("allocator returned wrong IP family")
		}

		if f := r.Used(); f != 0 {
			t.Errorf("Test %s unexpected used %d", tc.name, f)
		}
		found := sets.NewString()
		count := 0
		for r.Free() > 0 {
			ip, err := r.AllocateNext()
			if err != nil {
				t.Fatalf("Test %s error @ %d: %v", tc.name, count, err)
			}
			count++
			if !cidr.Contains(ip) {
				t.Fatalf("Test %s allocated %s which is outside of %s", tc.name, ip, cidr)
			}
			if found.Has(ip.String()) {
				t.Fatalf("Test %s allocated %s twice @ %d", tc.name, ip, count)
			}
			found.Insert(ip.String())
		}
		if _, err := r.AllocateNext(); err != ErrFull {
			t.Fatal(err)
		}

		released := net.ParseIP(tc.released)
		if err := r.Release(released); err != nil {
			t.Fatal(err)
		}
		if f := r.Free(); f != 1 {
			t.Errorf("Test %s unexpected free %d", tc.name, f)
		}
		if f := r.Used(); f != (tc.free - 1) {
			t.Errorf("Test %s unexpected free %d", tc.name, f)
		}
		ip, err := r.AllocateNext()
		if err != nil {
			t.Fatal(err)
		}
		if !released.Equal(ip) {
			t.Errorf("Test %s unexpected %s : %s", tc.name, ip, released)
		}

		if err := r.Release(released); err != nil {
			t.Fatal(err)
		}
		for _, outOfRange := range tc.outOfRange {
			err = r.Allocate(net.ParseIP(outOfRange))
			if _, ok := err.(*ErrNotInRange); !ok {
				t.Fatal(err)
			}
		}
		if err := r.Allocate(net.ParseIP(tc.alreadyAllocated)); err != ErrAllocated {
			t.Fatal(err)
		}
		if f := r.Free(); f != 1 {
			t.Errorf("Test %s unexpected free %d", tc.name, f)
		}
		if f := r.Used(); f != (tc.free - 1) {
			t.Errorf("Test %s unexpected free %d", tc.name, f)
		}
		if err := r.Allocate(released); err != nil {
			t.Fatal(err)
		}
		if f := r.Free(); f != 0 {
			t.Errorf("Test %s unexpected free %d", tc.name, f)
		}
		if f := r.Used(); f != tc.free {
			t.Errorf("Test %s unexpected free %d", tc.name, f)
		}
	}
}

func TestAllocateTiny(t *testing.T) {
	_, cidr, err := net.ParseCIDR("192.168.1.0/32")
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewInMemory(cidr)
	if err != nil {
		t.Fatal(err)
	}
	if f := r.Free(); f != 0 {
		t.Errorf("free: %d", f)
	}
	if _, err := r.AllocateNext(); err != ErrFull {
		t.Error(err)
	}
}

func TestAllocateSmall(t *testing.T) {
	_, cidr, err := net.ParseCIDR("192.168.1.240/30")
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewInMemory(cidr)
	if err != nil {
		t.Fatal(err)
	}
	if f := r.Free(); f != 2 {
		t.Errorf("free: %d", f)
	}
	found := sets.NewString()
	for i := 0; i < 2; i++ {
		ip, err := r.AllocateNext()
		if err != nil {
			t.Fatal(err)
		}
		if found.Has(ip.String()) {
			t.Fatalf("already reserved: %s", ip)
		}
		found.Insert(ip.String())
	}
	for s := range found {
		if !r.Has(net.ParseIP(s)) {
			t.Fatalf("missing: %s", s)
		}
		if err := r.Allocate(net.ParseIP(s)); err != ErrAllocated {
			t.Fatal(err)
		}
	}
	for i := 0; i < 100; i++ {
		if _, err := r.AllocateNext(); err != ErrFull {
			t.Fatalf("suddenly became not-full: %#v", r)
		}
	}

	if r.Free() != 0 && r.max != 2 {
		t.Fatalf("unexpected range: %v", r)
	}

	t.Logf("allocated: %v", found)
}

func TestForEach(t *testing.T) {
	_, cidr, err := net.ParseCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}

	testCases := []sets.String{
		sets.NewString(),
		sets.NewString("192.168.1.1"),
		sets.NewString("192.168.1.1", "192.168.1.254"),
		sets.NewString("192.168.1.1", "192.168.1.128", "192.168.1.254"),
	}

	for i, tc := range testCases {
		r, err := NewInMemory(cidr)
		if err != nil {
			t.Fatal(err)
		}
		for ips := range tc {
			ip := net.ParseIP(ips)
			if err := r.Allocate(ip); err != nil {
				t.Errorf("[%d] error allocating IP %v: %v", i, ip, err)
			}
			if !r.Has(ip) {
				t.Errorf("[%d] expected IP %v allocated", i, ip)
			}
		}
		calls := sets.NewString()
		r.ForEach(func(ip net.IP) {
			calls.Insert(ip.String())
		})
		if len(calls) != len(tc) {
			t.Errorf("[%d] expected %d calls, got %d", i, len(tc), len(calls))
		}
		if !calls.Equal(tc) {
			t.Errorf("[%d] expected calls to equal testcase: %v vs %v", i, calls.List(), tc.List())
		}
	}
}

func TestSnapshot(t *testing.T) {
	_, cidr, err := net.ParseCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewInMemory(cidr)
	if err != nil {
		t.Fatal(err)
	}
	ip := []net.IP{}
	for i := 0; i < 10; i++ {
		n, err := r.AllocateNext()
		if err != nil {
			t.Fatal(err)
		}
		ip = append(ip, n)
	}

	var dst api.RangeAllocation
	err = r.Snapshot(&dst)
	if err != nil {
		t.Fatal(err)
	}

	_, network, err := net.ParseCIDR(dst.Range)
	if err != nil {
		t.Fatal(err)
	}

	if !network.IP.Equal(cidr.IP) || network.Mask.String() != cidr.Mask.String() {
		t.Fatalf("mismatched networks: %s : %s", network, cidr)
	}

	_, otherCidr, err := net.ParseCIDR("192.168.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewInMemory(otherCidr)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Restore(otherCidr, dst.Data); err != ErrMismatchedNetwork {
		t.Fatal(err)
	}
	other, err := NewInMemory(network)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Restore(network, dst.Data); err != nil {
		t.Fatal(err)
	}

	for _, n := range ip {
		if !other.Has(n) {
			t.Errorf("restored range does not have %s", n)
		}
	}
	if other.Free() != r.Free() {
		t.Errorf("counts do not match: %d", other.Free())
	}
}

func TestNewFromSnapshot(t *testing.T) {
	_, cidr, err := net.ParseCIDR("192.168.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewInMemory(cidr)
	if err != nil {
		t.Fatal(err)
	}
	allocated := []net.IP{}
	for i := 0; i < 128; i++ {
		ip, err := r.AllocateNext()
		if err != nil {
			t.Fatal(err)
		}
		allocated = append(allocated, ip)
	}

	snapshot := api.RangeAllocation{}
	if err = r.Snapshot(&snapshot); err != nil {
		t.Fatal(err)
	}

	r, err = NewFromSnapshot(&snapshot)
	if err != nil {
		t.Fatal(err)
	}

	if x := r.Free(); x != 126 {
		t.Fatalf("expected 126 free IPs, got %d", x)
	}
	if x := r.Used(); x != 128 {
		t.Fatalf("expected 128 used IPs, got %d", x)
	}

	for _, ip := range allocated {
		if !r.Has(ip) {
			t.Fatalf("expected IP to be allocated, but it was not")
		}
	}
}

func TestClusterIPMetrics(t *testing.T) {
	// create IPv4 allocator
	cidrIPv4 := "10.0.0.0/24"
	_, clusterCIDRv4, _ := net.ParseCIDR(cidrIPv4)
	a, err := NewInMemory(clusterCIDRv4)
	if err != nil {
		t.Fatalf("unexpected error creating CidrSet: %v", err)
	}
	clearMetrics(map[string]string{"cidr": cidrIPv4})
	// create IPv6 allocator
	cidrIPv6 := "2001:db8::/112"
	_, clusterCIDRv6, _ := net.ParseCIDR(cidrIPv6)
	b, err := NewInMemory(clusterCIDRv6)
	if err != nil {
		t.Fatalf("unexpected error creating CidrSet: %v", err)
	}
	clearMetrics(map[string]string{"cidr": cidrIPv6})

	// Check initial state
	em := testMetrics{
		free:      0,
		used:      0,
		allocated: 0,
		errors:    0,
	}
	expectMetrics(t, cidrIPv4, em)
	em = testMetrics{
		free:      0,
		used:      0,
		allocated: 0,
		errors:    0,
	}
	expectMetrics(t, cidrIPv6, em)

	// allocate 2 IPv4 addresses
	found := sets.NewString()
	for i := 0; i < 2; i++ {
		ip, err := a.AllocateNext()
		if err != nil {
			t.Fatal(err)
		}
		if found.Has(ip.String()) {
			t.Fatalf("already reserved: %s", ip)
		}
		found.Insert(ip.String())
	}

	em = testMetrics{
		free:      252,
		used:      2,
		allocated: 2,
		errors:    0,
	}
	expectMetrics(t, cidrIPv4, em)

	// try to allocate the same IP addresses
	for s := range found {
		if !a.Has(net.ParseIP(s)) {
			t.Fatalf("missing: %s", s)
		}
		if err := a.Allocate(net.ParseIP(s)); err != ErrAllocated {
			t.Fatal(err)
		}
	}
	em = testMetrics{
		free:      252,
		used:      2,
		allocated: 2,
		errors:    2,
	}
	expectMetrics(t, cidrIPv4, em)

	// release the addresses allocated
	for s := range found {
		if !a.Has(net.ParseIP(s)) {
			t.Fatalf("missing: %s", s)
		}
		if err := a.Release(net.ParseIP(s)); err != nil {
			t.Fatal(err)
		}
	}
	em = testMetrics{
		free:      254,
		used:      0,
		allocated: 2,
		errors:    2,
	}
	expectMetrics(t, cidrIPv4, em)

	// allocate 264 addresses for each allocator
	// the full range and 10 more (254 + 10 = 264) for IPv4
	for i := 0; i < 264; i++ {
		a.AllocateNext()
		b.AllocateNext()
	}
	em = testMetrics{
		free:      0,
		used:      254,
		allocated: 256, // this is a counter, we already had 2 allocations and we did 254 more
		errors:    12,
	}
	expectMetrics(t, cidrIPv4, em)
	em = testMetrics{
		free:      65271, // IPv6 clusterIP range is capped to 2^16 and consider the broadcast address as valid
		used:      264,
		allocated: 264,
		errors:    0,
	}
	expectMetrics(t, cidrIPv6, em)
}

// Metrics helpers
func clearMetrics(labels map[string]string) {
	clusterIPAllocated.Delete(labels)
	clusterIPAvailable.Delete(labels)
	clusterIPAllocations.Delete(labels)
	clusterIPAllocationErrors.Delete(labels)
}

type testMetrics struct {
	free      float64
	used      float64
	allocated float64
	errors    float64
}

func expectMetrics(t *testing.T, label string, em testMetrics) {
	var m testMetrics
	var err error
	m.free, err = testutil.GetGaugeMetricValue(clusterIPAvailable.WithLabelValues(label))
	if err != nil {
		t.Errorf("failed to get %s value, err: %v", clusterIPAvailable.Name, err)
	}
	m.used, err = testutil.GetGaugeMetricValue(clusterIPAllocated.WithLabelValues(label))
	if err != nil {
		t.Errorf("failed to get %s value, err: %v", clusterIPAllocated.Name, err)
	}
	m.allocated, err = testutil.GetCounterMetricValue(clusterIPAllocations.WithLabelValues(label))
	if err != nil {
		t.Errorf("failed to get %s value, err: %v", clusterIPAllocations.Name, err)
	}
	m.errors, err = testutil.GetCounterMetricValue(clusterIPAllocationErrors.WithLabelValues(label))
	if err != nil {
		t.Errorf("failed to get %s value, err: %v", clusterIPAllocationErrors.Name, err)
	}

	if m != em {
		t.Fatalf("metrics error: expected %v, received %v", em, m)
	}
}
