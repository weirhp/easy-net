#include <winsock2.h>
#include <ws2tcpip.h>

#include <cassert>
#include <cstdint>
#include <vector>

#include "../src/socks5_protocol.h"

int main() {
    sockaddr_in ipv4{};
    ipv4.sin_family = AF_INET;
    ipv4.sin_port = htons(443);
    assert(InetPtonA(AF_INET, "203.0.113.7", &ipv4.sin_addr) == 1);

    std::vector<std::uint8_t> request;
    assert(easy_net::socks5::BuildConnectRequest(
        reinterpret_cast<const sockaddr*>(&ipv4), sizeof(ipv4), request));
    const std::vector<std::uint8_t> expected_ipv4{
        0x05, 0x01, 0x00, 0x01, 203, 0, 113, 7, 0x01, 0xbb};
    assert(request == expected_ipv4);

    sockaddr_in6 ipv6{};
    ipv6.sin6_family = AF_INET6;
    ipv6.sin6_port = htons(80);
    assert(InetPtonA(AF_INET6, "2001:db8::1", &ipv6.sin6_addr) == 1);
    assert(easy_net::socks5::BuildConnectRequest(
        reinterpret_cast<const sockaddr*>(&ipv6), sizeof(ipv6), request));
    assert(request.size() == 22);
    assert(request[0] == 0x05 && request[1] == 0x01 && request[3] == 0x04);
    assert(request[20] == 0x00 && request[21] == 0x50);

    sockaddr unsupported{};
    unsupported.sa_family = AF_UNSPEC;
    assert(!easy_net::socks5::BuildConnectRequest(&unsupported, sizeof(unsupported), request));
    assert(request.empty());

    assert(easy_net::socks5::ReplyToWsaError(0x02) == WSAEACCES);
    assert(easy_net::socks5::ReplyToWsaError(0x05) == WSAECONNREFUSED);
    assert(easy_net::socks5::ReplyToWsaError(0xff) == WSAECONNABORTED);
    return 0;
}
