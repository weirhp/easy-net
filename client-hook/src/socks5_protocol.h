#pragma once

#include <winsock2.h>
#include <ws2tcpip.h>

#include <cstdint>
#include <vector>

namespace easy_net::socks5 {

inline bool BuildConnectRequest(const sockaddr* destination,
                                int destination_length,
                                std::vector<std::uint8_t>& request) {
    request.clear();
    if (destination == nullptr || destination_length < static_cast<int>(sizeof(sockaddr))) {
        return false;
    }

    request.push_back(0x05);
    request.push_back(0x01);
    request.push_back(0x00);

    if (destination->sa_family == AF_INET &&
        destination_length >= static_cast<int>(sizeof(sockaddr_in))) {
        const auto* address = reinterpret_cast<const sockaddr_in*>(destination);
        request.push_back(0x01);
        const auto* bytes = reinterpret_cast<const std::uint8_t*>(&address->sin_addr);
        request.insert(request.end(), bytes, bytes + sizeof(address->sin_addr));
        const auto* port = reinterpret_cast<const std::uint8_t*>(&address->sin_port);
        request.insert(request.end(), port, port + sizeof(address->sin_port));
        return true;
    }

    if (destination->sa_family == AF_INET6 &&
        destination_length >= static_cast<int>(sizeof(sockaddr_in6))) {
        const auto* address = reinterpret_cast<const sockaddr_in6*>(destination);
        request.push_back(0x04);
        const auto* bytes = reinterpret_cast<const std::uint8_t*>(&address->sin6_addr);
        request.insert(request.end(), bytes, bytes + sizeof(address->sin6_addr));
        const auto* port = reinterpret_cast<const std::uint8_t*>(&address->sin6_port);
        request.insert(request.end(), port, port + sizeof(address->sin6_port));
        return true;
    }

    request.clear();
    return false;
}

inline int ReplyToWsaError(std::uint8_t reply) {
    switch (reply) {
        case 0x01:
            return WSAECONNREFUSED;
        case 0x02:
            return WSAEACCES;
        case 0x03:
            return WSAENETUNREACH;
        case 0x04:
            return WSAEHOSTUNREACH;
        case 0x05:
            return WSAECONNREFUSED;
        case 0x06:
            return WSAETIMEDOUT;
        case 0x07:
            return WSAEOPNOTSUPP;
        case 0x08:
            return WSAEAFNOSUPPORT;
        default:
            return WSAECONNABORTED;
    }
}

}  // namespace easy_net::socks5
