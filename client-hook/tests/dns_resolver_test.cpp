#include <winsock2.h>
#include <ws2tcpip.h>

#include <cassert>
#include <cstdint>
#include <vector>

#include "../src/dns_resolver.h"

namespace {

void AppendUint16(std::vector<std::uint8_t>& data, std::uint16_t value) {
    data.push_back(static_cast<std::uint8_t>(value >> 8U));
    data.push_back(static_cast<std::uint8_t>(value & 0xffU));
}

}  // namespace

int main() {
    easy_net::dns::Endpoint endpoint;
    assert(easy_net::dns::ParseEndpoint(L"223.5.5.5", endpoint));
    const auto* ipv4 = reinterpret_cast<const sockaddr_in*>(&endpoint.address);
    assert(ipv4->sin_family == AF_INET);
    assert(ntohs(ipv4->sin_port) == 53);
    assert(easy_net::dns::ParseEndpoint(L"[2001:4860:4860::8888]:5353", endpoint));
    const auto* ipv6 = reinterpret_cast<const sockaddr_in6*>(&endpoint.address);
    assert(ipv6->sin6_family == AF_INET6);
    assert(ntohs(ipv6->sin6_port) == 5353);
    assert(!easy_net::dns::ParseEndpoint(L"dns.example:53", endpoint));

    constexpr std::uint16_t transaction_id = 0x1234;
    std::vector<std::uint8_t> query;
    assert(easy_net::dns::BuildQuery("example.com", easy_net::dns::kTypeA,
                                     transaction_id, query));
    assert(query.size() == 29);
    assert(query[0] == 0x12 && query[1] == 0x34);
    assert(query[12] == 7 && query[20] == 3 && query[24] == 0);
    assert(!easy_net::dns::BuildQuery("bad..example", easy_net::dns::kTypeA,
                                      transaction_id, query));

    assert(easy_net::dns::BuildQuery("example.com", easy_net::dns::kTypeA,
                                     transaction_id, query));
    std::vector<std::uint8_t> response = query;
    response[2] = 0x81;
    response[3] = 0x80;
    response[6] = 0x00;
    response[7] = 0x01;
    response.push_back(0xc0);
    response.push_back(0x0c);
    AppendUint16(response, easy_net::dns::kTypeA);
    AppendUint16(response, 1);
    response.insert(response.end(), {0x00, 0x00, 0x00, 0x3c});
    AppendUint16(response, 4);
    response.insert(response.end(), {203, 0, 113, 9});

    std::vector<easy_net::dns::Address> addresses;
    bool truncated = false;
    assert(easy_net::dns::ParseResponse(response.data(), response.size(), transaction_id,
                                        easy_net::dns::kTypeA, addresses, truncated) == 0);
    assert(!truncated);
    assert(addresses.size() == 1);
    assert(addresses[0].family == AF_INET);
    assert(addresses[0].bytes[0] == 203 && addresses[0].bytes[3] == 9);

    std::vector<std::uint8_t> aaaa_query;
    assert(easy_net::dns::BuildQuery("example.com", easy_net::dns::kTypeAaaa,
                                     transaction_id, aaaa_query));
    std::vector<std::uint8_t> aaaa_response = aaaa_query;
    aaaa_response[2] = 0x81;
    aaaa_response[3] = 0x80;
    aaaa_response[6] = 0x00;
    aaaa_response[7] = 0x01;
    aaaa_response.insert(aaaa_response.end(), {0xc0, 0x0c});
    AppendUint16(aaaa_response, easy_net::dns::kTypeAaaa);
    AppendUint16(aaaa_response, 1);
    aaaa_response.insert(aaaa_response.end(), {0x00, 0x00, 0x00, 0x3c});
    AppendUint16(aaaa_response, 16);
    aaaa_response.insert(aaaa_response.end(),
                         {0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1});
    addresses.clear();
    assert(easy_net::dns::ParseResponse(aaaa_response.data(), aaaa_response.size(), transaction_id,
                                        easy_net::dns::kTypeAaaa, addresses, truncated) == 0);
    assert(addresses.size() == 1 && addresses[0].family == AF_INET6);
    assert(addresses[0].bytes[0] == 0x20 && addresses[0].bytes[15] == 1);

    response[2] |= 0x02;
    addresses.clear();
    assert(easy_net::dns::ParseResponse(response.data(), response.size(), transaction_id,
                                        easy_net::dns::kTypeA, addresses, truncated) == WSATRY_AGAIN);
    assert(truncated);
    return 0;
}
