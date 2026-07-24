// SPDX-License-Identifier: GPL-2.0
//
// decision_tests.c — SYSCALL-type wrapper programs for exercising the
// production routing-decision helpers in common.h under BPF_PROG_TEST_RUN
// (the cilium bpf/tests pattern). Each wrapper reads its inputs from the
// single-entry test_scratch array map, calls the production helper against
// the real (unpinned, test-loaded) maps, and writes the result back to
// test_scratch for the Go harness to assert on.
//
// The scratch-map calling convention (rather than prog_run ctx_in) keeps the
// wrappers portable: BPF_PROG_TYPE_SYSCALL is runnable on any kernel ≥ 5.14,
// with no dependency on per-prog-type ctx marshaling support (cgroup
// sock_addr progs, for example, only gained test_run support much later).
//
// This file is compiled by bpf2go from the bpftest package's gen.go and the
// bindings are gitignored, exactly like the production clawker.c bindings.

#include "../common.h"

// test_scratch — single-entry I/O block between the Go harness and the
// wrapper programs. The harness writes the in_* fields, runs a wrapper,
// then reads the out_* fields. One struct for all wrappers keeps the
// harness to a single map handle; each wrapper documents which fields it
// touches.
struct test_scratch {
	// Inputs.
	__u32 in_dst_ip;        // network byte order, dns_cache key
	__u32 in_identity;      // route_map key part
	__u16 in_dst_port;      // host byte order, route_map key part
	__u8  in_l4_proto;      // SOCK_STREAM / SOCK_DGRAM, route_map key part
	__u8  _pad0;
	__u64 in_cookie;        // udp_flow_map key part (0 = must not record)
	__u32 in_backend_ip;    // network byte order, udp_flow_map key part
	__u16 in_backend_port;  // host byte order, udp_flow_map key part
	__u16 _pad1;
	// Outputs.
	__u32 out_identity;     // lookup_identity_for_ip result
	__u16 out_envoy_port;   // lookup_route result (0 = miss)
	__u16 _pad2;
};

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct test_scratch);
} test_scratch_map SEC(".maps");

static __always_inline struct test_scratch *scratch(void)
{
	__u32 zero = 0;
	return bpf_map_lookup_elem(&test_scratch_map, &zero);
}

// test_lookup_identity_for_ip: in_dst_ip → out_identity.
// Exercises the dns_cache read the connect4/sendmsg4 fast path performs on
// every managed connect()/sendto().
SEC("syscall")
int test_lookup_identity_for_ip(void *ctx)
{
	struct test_scratch *s = scratch();
	if (!s)
		return 1;
	s->out_identity = lookup_identity_for_ip(s->in_dst_ip);
	return 0;
}

// test_lookup_route: {in_identity, in_dst_port, in_l4_proto} → out_envoy_port.
// Exercises the route_map read that selects the per-domain Envoy listener.
SEC("syscall")
int test_lookup_route(void *ctx)
{
	struct test_scratch *s = scratch();
	if (!s)
		return 1;
	s->out_envoy_port = lookup_route(s->in_identity, s->in_dst_port,
					 s->in_l4_proto);
	return 0;
}

// test_route_for_dst: the composed decision chain dns_cache → route_map,
// exactly as decide_connect performs it: resolve in_dst_ip to an identity,
// then key route_map with {identity, in_dst_port, in_l4_proto}. Writes both
// out_identity and out_envoy_port. This is the keyspace-aliasing regression
// surface: two destinations must never resolve through each other's route.
SEC("syscall")
int test_route_for_dst(void *ctx)
{
	struct test_scratch *s = scratch();
	if (!s)
		return 1;
	s->out_identity = lookup_identity_for_ip(s->in_dst_ip);
	s->out_envoy_port = lookup_route(s->out_identity, s->in_dst_port,
					 s->in_l4_proto);
	return 0;
}

// test_record_udp_flow: {in_cookie, in_backend_ip, in_backend_port} →
// udp_flow_map[{cookie, backend}] = {in_dst_ip, in_dst_port}. The harness
// asserts the map entry directly (including the cookie==0 skip and the
// network-byte-order port conversion record_udp_flow owns).
SEC("syscall")
int test_record_udp_flow(void *ctx)
{
	struct test_scratch *s = scratch();
	if (!s)
		return 1;
	record_udp_flow(s->in_cookie, s->in_backend_ip, s->in_backend_port,
			s->in_dst_ip, s->in_dst_port);
	return 0;
}

char _license[] SEC("license") = "GPL";
