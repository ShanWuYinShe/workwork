"""Tests for IP address management."""

import pytest
from dev.ipam import ip_to_int, int_to_ip


class TestIPConversion:
    def test_ip_to_int_roundtrip(self):
        ips = ["172.18.0.1", "10.0.0.1", "192.168.1.255", "0.0.0.0", "255.255.255.255"]
        for ip in ips:
            assert int_to_ip(ip_to_int(ip)) == ip

    def test_ip_to_int_invalid(self):
        with pytest.raises(ValueError):
            ip_to_int("not-an-ip")
        with pytest.raises(ValueError):
            ip_to_int("1.2.3")
        with pytest.raises(ValueError):
            ip_to_int("")

    def test_int_to_ip_format(self):
        assert int_to_ip(0) == "0.0.0.0"
        assert int_to_ip(0xFFFFFFFF) == "255.255.255.255"
        assert int_to_ip(3232235521) == "192.168.0.1"

    def test_ip_ordering(self):
        assert ip_to_int("172.18.0.51") < ip_to_int("172.18.0.52")
        assert ip_to_int("172.18.0.255") < ip_to_int("172.18.1.0")

    def test_ip_range(self):
        start = ip_to_int("172.18.0.51")
        end = ip_to_int("172.18.0.55")
        ips = [int_to_ip(i) for i in range(start, end + 1)]
        assert ips == [
            "172.18.0.51", "172.18.0.52",
            "172.18.0.53", "172.18.0.54",
            "172.18.0.55",
        ]
