#pragma once

#include <winsock2.h>
#include <ws2tcpip.h>

#include <cstddef>
#include <cstdint>

#include "tun_config.h"

namespace easy_net::socks5_health {

inline bool SendAll(SOCKET socket, const std::uint8_t* data, std::size_t size) {
    std::size_t sent = 0;
    while (sent < size) {
        const int count = send(socket, reinterpret_cast<const char*>(data + sent),
                               static_cast<int>(size - sent), 0);
        if (count <= 0) return false;
        sent += static_cast<std::size_t>(count);
    }
    return true;
}

inline bool ReceiveAll(SOCKET socket, std::uint8_t* data, std::size_t size) {
    std::size_t received = 0;
    while (received < size) {
        const int count = recv(socket, reinterpret_cast<char*>(data + received),
                               static_cast<int>(size - received), 0);
        if (count <= 0) return false;
        received += static_cast<std::size_t>(count);
    }
    return true;
}

inline bool Responsive(const easy_net::tun::Endpoint& endpoint, DWORD timeout_ms = 1000) {
    WSADATA winsock{};
    if (WSAStartup(MAKEWORD(2, 2), &winsock) != 0) return false;
    sockaddr_storage address{};
    int address_length = 0;
    int family = AF_UNSPEC;
    if (InetPtonA(AF_INET, endpoint.host.c_str(),
                  &reinterpret_cast<sockaddr_in*>(&address)->sin_addr) == 1) {
        auto* ipv4 = reinterpret_cast<sockaddr_in*>(&address);
        ipv4->sin_family = AF_INET;
        ipv4->sin_port = htons(endpoint.port);
        address_length = sizeof(*ipv4);
        family = AF_INET;
    } else if (InetPtonA(AF_INET6, endpoint.host.c_str(),
                         &reinterpret_cast<sockaddr_in6*>(&address)->sin6_addr) == 1) {
        auto* ipv6 = reinterpret_cast<sockaddr_in6*>(&address);
        ipv6->sin6_family = AF_INET6;
        ipv6->sin6_port = htons(endpoint.port);
        address_length = sizeof(*ipv6);
        family = AF_INET6;
    } else {
        WSACleanup();
        return false;
    }
    const SOCKET socket = ::socket(family, SOCK_STREAM, IPPROTO_TCP);
    if (socket == INVALID_SOCKET) {
        WSACleanup();
        return false;
    }
    bool healthy = false;
    do {
        u_long nonblocking = 1;
        if (ioctlsocket(socket, FIONBIO, &nonblocking) != 0) break;
        const int connected = connect(socket, reinterpret_cast<const sockaddr*>(&address),
                                      address_length);
        if (connected == SOCKET_ERROR && WSAGetLastError() != WSAEWOULDBLOCK) break;
        if (connected == SOCKET_ERROR) {
            fd_set writable;
            fd_set failed;
            FD_ZERO(&writable);
            FD_ZERO(&failed);
            FD_SET(socket, &writable);
            FD_SET(socket, &failed);
            timeval timeout{static_cast<long>(timeout_ms / 1000),
                            static_cast<long>((timeout_ms % 1000) * 1000)};
            if (select(0, nullptr, &writable, &failed, &timeout) <= 0 ||
                FD_ISSET(socket, &failed)) break;
            int socket_error = 0;
            int socket_error_size = sizeof(socket_error);
            if (getsockopt(socket, SOL_SOCKET, SO_ERROR,
                           reinterpret_cast<char*>(&socket_error), &socket_error_size) != 0 ||
                socket_error != 0) break;
        }
        nonblocking = 0;
        if (ioctlsocket(socket, FIONBIO, &nonblocking) != 0) break;
        setsockopt(socket, SOL_SOCKET, SO_RCVTIMEO,
                   reinterpret_cast<const char*>(&timeout_ms), sizeof(timeout_ms));
        setsockopt(socket, SOL_SOCKET, SO_SNDTIMEO,
                   reinterpret_cast<const char*>(&timeout_ms), sizeof(timeout_ms));
        const std::uint8_t greeting[]{5, 2, 0, 2};
        std::uint8_t reply[2]{};
        healthy = SendAll(socket, greeting, sizeof(greeting)) &&
                  ReceiveAll(socket, reply, sizeof(reply)) && reply[0] == 5 && reply[1] != 0xff;
    } while (false);
    closesocket(socket);
    WSACleanup();
    return healthy;
}

}  // namespace easy_net::socks5_health
