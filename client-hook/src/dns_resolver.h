#pragma once

#include <winsock2.h>
#include <ws2tcpip.h>

#include <array>
#include <cstddef>
#include <cstdint>
#include <string_view>
#include <vector>

namespace easy_net::dns {

constexpr std::uint16_t kTypeA = 1;
constexpr std::uint16_t kTypeAaaa = 28;

struct Endpoint {
    sockaddr_storage address{};
    int length = 0;
};

struct Address {
    int family = AF_UNSPEC;
    std::array<std::uint8_t, 16> bytes{};

    bool operator==(const Address& other) const {
        return family == other.family && bytes == other.bytes;
    }
};

struct SocketApi {
    decltype(&connect) connect_socket = nullptr;
    decltype(&send) send_data = nullptr;
    decltype(&recv) receive_data = nullptr;
    decltype(&sendto) send_to = nullptr;
    decltype(&recvfrom) receive_from = nullptr;
    decltype(&closesocket) close_socket = nullptr;
};

bool ParseEndpoint(std::wstring_view value, Endpoint& output);

bool BuildQuery(std::string_view hostname,
                std::uint16_t query_type,
                std::uint16_t transaction_id,
                std::vector<std::uint8_t>& output);

int ParseResponse(const std::uint8_t* data,
                  std::size_t size,
                  std::uint16_t transaction_id,
                  std::uint16_t query_type,
                  std::vector<Address>& output,
                  bool& truncated);

int Resolve(const Endpoint& server,
            std::string_view hostname,
            int family,
            const SocketApi& socket_api,
            std::vector<Address>& output);

}  // namespace easy_net::dns
