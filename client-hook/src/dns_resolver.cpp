#include "dns_resolver.h"

#include <windows.h>

#include <algorithm>
#include <array>
#include <cstdlib>
#include <cwchar>
#include <cstring>
#include <limits>
#include <string>

namespace easy_net::dns {
namespace {

constexpr int kTimeoutMilliseconds = 3000;
volatile LONG g_transaction_counter = 0;

std::uint16_t ReadUint16(const std::uint8_t* data) {
    return static_cast<std::uint16_t>((static_cast<std::uint16_t>(data[0]) << 8U) |
                                      static_cast<std::uint16_t>(data[1]));
}

void AppendUint16(std::vector<std::uint8_t>& data, std::uint16_t value) {
    data.push_back(static_cast<std::uint8_t>(value >> 8U));
    data.push_back(static_cast<std::uint8_t>(value & 0xffU));
}

bool ParsePort(const std::wstring& text, std::uint16_t& port) {
    if (text.empty()) {
        return false;
    }
    wchar_t* end = nullptr;
    const long parsed = std::wcstol(text.c_str(), &end, 10);
    if (parsed <= 0 || parsed > 65535 || end == nullptr || *end != L'\0') {
        return false;
    }
    port = static_cast<std::uint16_t>(parsed);
    return true;
}

bool SkipName(const std::uint8_t* data, std::size_t size, std::size_t& offset) {
    std::size_t labels = 0;
    while (offset < size && labels <= 127) {
        const std::uint8_t length = data[offset];
        if ((length & 0xc0U) == 0xc0U) {
            if (offset + 2 > size) {
                return false;
            }
            const std::size_t pointer =
                (static_cast<std::size_t>(length & 0x3fU) << 8U) | data[offset + 1];
            if (pointer >= size) {
                return false;
            }
            offset += 2;
            return true;
        }
        if ((length & 0xc0U) != 0) {
            return false;
        }
        ++offset;
        if (length == 0) {
            return true;
        }
        if (length > 63 || offset + length > size) {
            return false;
        }
        offset += length;
        ++labels;
    }
    return false;
}

bool SetSocketTimeouts(SOCKET socket) {
    const int timeout = kTimeoutMilliseconds;
    return setsockopt(socket, SOL_SOCKET, SO_RCVTIMEO,
                      reinterpret_cast<const char*>(&timeout), sizeof(timeout)) == 0 &&
           setsockopt(socket, SOL_SOCKET, SO_SNDTIMEO,
                      reinterpret_cast<const char*>(&timeout), sizeof(timeout)) == 0;
}

bool SameEndpoint(const sockaddr* left, int left_length, const sockaddr* right, int right_length) {
    if (left == nullptr || right == nullptr || left->sa_family != right->sa_family) {
        return false;
    }
    if (left->sa_family == AF_INET && left_length >= static_cast<int>(sizeof(sockaddr_in)) &&
        right_length >= static_cast<int>(sizeof(sockaddr_in))) {
        const auto* a = reinterpret_cast<const sockaddr_in*>(left);
        const auto* b = reinterpret_cast<const sockaddr_in*>(right);
        return a->sin_port == b->sin_port && a->sin_addr.s_addr == b->sin_addr.s_addr;
    }
    if (left->sa_family == AF_INET6 && left_length >= static_cast<int>(sizeof(sockaddr_in6)) &&
        right_length >= static_cast<int>(sizeof(sockaddr_in6))) {
        const auto* a = reinterpret_cast<const sockaddr_in6*>(left);
        const auto* b = reinterpret_cast<const sockaddr_in6*>(right);
        return a->sin6_port == b->sin6_port && a->sin6_scope_id == b->sin6_scope_id &&
               std::memcmp(&a->sin6_addr, &b->sin6_addr, sizeof(in6_addr)) == 0;
    }
    return false;
}

bool SendAll(SOCKET socket,
             const SocketApi& api,
             const std::uint8_t* data,
             std::size_t size) {
    std::size_t offset = 0;
    while (offset < size) {
        const int sent = api.send_data(socket, reinterpret_cast<const char*>(data + offset),
                                       static_cast<int>(size - offset), 0);
        if (sent == SOCKET_ERROR || sent == 0) {
            return false;
        }
        offset += static_cast<std::size_t>(sent);
    }
    return true;
}

bool ReceiveExact(SOCKET socket,
                  const SocketApi& api,
                  std::uint8_t* data,
                  std::size_t size) {
    std::size_t offset = 0;
    while (offset < size) {
        const int received = api.receive_data(socket, reinterpret_cast<char*>(data + offset),
                                              static_cast<int>(size - offset), 0);
        if (received == SOCKET_ERROR || received == 0) {
            return false;
        }
        offset += static_cast<std::size_t>(received);
    }
    return true;
}

int QueryTcp(const Endpoint& server,
             const SocketApi& api,
             const std::vector<std::uint8_t>& query,
             std::uint16_t transaction_id,
             std::uint16_t query_type,
             std::vector<Address>& output) {
    SOCKET socket = ::socket(reinterpret_cast<const sockaddr*>(&server.address)->sa_family,
                             SOCK_STREAM, IPPROTO_TCP);
    if (socket == INVALID_SOCKET) {
        return WSATRY_AGAIN;
    }
    SetSocketTimeouts(socket);

    int result = WSATRY_AGAIN;
    if (api.connect_socket(socket, reinterpret_cast<const sockaddr*>(&server.address), server.length) == 0) {
        std::vector<std::uint8_t> framed;
        framed.reserve(query.size() + 2);
        AppendUint16(framed, static_cast<std::uint16_t>(query.size()));
        framed.insert(framed.end(), query.begin(), query.end());
        std::array<std::uint8_t, 2> length_bytes{};
        if (SendAll(socket, api, framed.data(), framed.size()) &&
            ReceiveExact(socket, api, length_bytes.data(), length_bytes.size())) {
            const std::size_t response_size = ReadUint16(length_bytes.data());
            if (response_size >= 12) {
                std::vector<std::uint8_t> response(response_size);
                if (ReceiveExact(socket, api, response.data(), response.size())) {
                    bool ignored_truncated = false;
                    result = ParseResponse(response.data(), response.size(), transaction_id,
                                           query_type, output, ignored_truncated);
                }
            }
        }
    }
    api.close_socket(socket);
    return result;
}

int QueryOnce(const Endpoint& server,
              const SocketApi& api,
              std::string_view hostname,
              std::uint16_t query_type,
              std::vector<Address>& output) {
    const std::uint16_t transaction_id = static_cast<std::uint16_t>(
        InterlockedIncrement(&g_transaction_counter) ^ GetCurrentThreadId() ^ GetTickCount());
    std::vector<std::uint8_t> query;
    if (!BuildQuery(hostname, query_type, transaction_id, query)) {
        return WSAHOST_NOT_FOUND;
    }

    SOCKET socket = ::socket(reinterpret_cast<const sockaddr*>(&server.address)->sa_family,
                             SOCK_DGRAM, IPPROTO_UDP);
    if (socket == INVALID_SOCKET) {
        return WSATRY_AGAIN;
    }
    SetSocketTimeouts(socket);

    int result = WSATRY_AGAIN;
    const int sent = api.send_to(socket, reinterpret_cast<const char*>(query.data()),
                                 static_cast<int>(query.size()), 0,
                                 reinterpret_cast<const sockaddr*>(&server.address), server.length);
    if (sent == static_cast<int>(query.size())) {
        std::array<std::uint8_t, 4096> response{};
        sockaddr_storage sender{};
        int sender_length = sizeof(sender);
        const int received = api.receive_from(socket, reinterpret_cast<char*>(response.data()),
                                              static_cast<int>(response.size()), 0,
                                              reinterpret_cast<sockaddr*>(&sender), &sender_length);
        if (received > 0 &&
            SameEndpoint(reinterpret_cast<const sockaddr*>(&sender), sender_length,
                         reinterpret_cast<const sockaddr*>(&server.address), server.length)) {
            bool truncated = false;
            result = ParseResponse(response.data(), static_cast<std::size_t>(received),
                                   transaction_id, query_type, output, truncated);
            if (truncated) {
                output.clear();
                result = QueryTcp(server, api, query, transaction_id, query_type, output);
            }
        }
    }
    api.close_socket(socket);
    return result;
}

}  // namespace

bool ParseEndpoint(std::wstring_view value, Endpoint& output) {
    if (value.empty()) {
        return false;
    }

    std::wstring host;
    std::uint16_t port = 53;
    const std::wstring complete(value);

    sockaddr_in direct_ipv4{};
    if (InetPtonW(AF_INET, complete.c_str(), &direct_ipv4.sin_addr) == 1) {
        direct_ipv4.sin_family = AF_INET;
        direct_ipv4.sin_port = htons(port);
        std::memcpy(&output.address, &direct_ipv4, sizeof(direct_ipv4));
        output.length = sizeof(direct_ipv4);
        return true;
    }
    sockaddr_in6 direct_ipv6{};
    if (InetPtonW(AF_INET6, complete.c_str(), &direct_ipv6.sin6_addr) == 1) {
        direct_ipv6.sin6_family = AF_INET6;
        direct_ipv6.sin6_port = htons(port);
        std::memcpy(&output.address, &direct_ipv6, sizeof(direct_ipv6));
        output.length = sizeof(direct_ipv6);
        return true;
    }

    if (complete.front() == L'[') {
        const std::size_t close = complete.find(L']');
        if (close == std::wstring::npos || close + 1 >= complete.size() || complete[close + 1] != L':') {
            return false;
        }
        host = complete.substr(1, close - 1);
        if (!ParsePort(complete.substr(close + 2), port)) {
            return false;
        }
    } else {
        const std::size_t colon = complete.rfind(L':');
        if (colon == std::wstring::npos || complete.find(L':') != colon) {
            return false;
        }
        host = complete.substr(0, colon);
        if (!ParsePort(complete.substr(colon + 1), port)) {
            return false;
        }
    }

    sockaddr_in ipv4{};
    ipv4.sin_family = AF_INET;
    ipv4.sin_port = htons(port);
    if (InetPtonW(AF_INET, host.c_str(), &ipv4.sin_addr) == 1) {
        std::memcpy(&output.address, &ipv4, sizeof(ipv4));
        output.length = sizeof(ipv4);
        return true;
    }

    sockaddr_in6 ipv6{};
    ipv6.sin6_family = AF_INET6;
    ipv6.sin6_port = htons(port);
    if (InetPtonW(AF_INET6, host.c_str(), &ipv6.sin6_addr) == 1) {
        std::memcpy(&output.address, &ipv6, sizeof(ipv6));
        output.length = sizeof(ipv6);
        return true;
    }
    return false;
}

bool BuildQuery(std::string_view hostname,
                std::uint16_t query_type,
                std::uint16_t transaction_id,
                std::vector<std::uint8_t>& output) {
    output.clear();
    while (!hostname.empty() && hostname.back() == '.') {
        hostname.remove_suffix(1);
    }
    if (hostname.empty() || hostname.size() > 253 ||
        (query_type != kTypeA && query_type != kTypeAaaa)) {
        return false;
    }

    output.reserve(hostname.size() + 18);
    AppendUint16(output, transaction_id);
    AppendUint16(output, 0x0100);  // Recursion desired.
    AppendUint16(output, 1);
    AppendUint16(output, 0);
    AppendUint16(output, 0);
    AppendUint16(output, 0);

    std::size_t start = 0;
    while (start < hostname.size()) {
        const std::size_t dot = hostname.find('.', start);
        const std::size_t end = dot == std::string_view::npos ? hostname.size() : dot;
        const std::size_t length = end - start;
        if (length == 0 || length > 63) {
            output.clear();
            return false;
        }
        output.push_back(static_cast<std::uint8_t>(length));
        for (std::size_t index = start; index < end; ++index) {
            const unsigned char character = static_cast<unsigned char>(hostname[index]);
            if (character > 0x7fU) {
                output.clear();
                return false;
            }
            output.push_back(character);
        }
        if (dot == std::string_view::npos) {
            break;
        }
        start = dot + 1;
    }
    output.push_back(0);
    AppendUint16(output, query_type);
    AppendUint16(output, 1);  // Internet class.
    return output.size() <= 512;
}

int ParseResponse(const std::uint8_t* data,
                  std::size_t size,
                  std::uint16_t transaction_id,
                  std::uint16_t query_type,
                  std::vector<Address>& output,
                  bool& truncated) {
    output.clear();
    truncated = false;
    if (data == nullptr || size < 12 || ReadUint16(data) != transaction_id ||
        (data[2] & 0x80U) == 0) {
        return WSATRY_AGAIN;
    }
    truncated = (data[2] & 0x02U) != 0;
    if (truncated) {
        return WSATRY_AGAIN;
    }

    const std::uint8_t response_code = data[3] & 0x0fU;
    if (response_code == 3) {
        return WSAHOST_NOT_FOUND;
    }
    if (response_code != 0) {
        return WSATRY_AGAIN;
    }

    const std::uint16_t question_count = ReadUint16(data + 4);
    const std::uint16_t answer_count = ReadUint16(data + 6);
    if (question_count != 1) {
        return WSATRY_AGAIN;
    }
    std::size_t offset = 12;
    for (std::uint16_t index = 0; index < question_count; ++index) {
        if (!SkipName(data, size, offset) || offset + 4 > size) {
            return WSATRY_AGAIN;
        }
        if (ReadUint16(data + offset) != query_type || ReadUint16(data + offset + 2) != 1) {
            return WSATRY_AGAIN;
        }
        offset += 4;
    }

    for (std::uint16_t index = 0; index < answer_count; ++index) {
        if (!SkipName(data, size, offset) || offset + 10 > size) {
            return WSATRY_AGAIN;
        }
        const std::uint16_t type = ReadUint16(data + offset);
        const std::uint16_t record_class = ReadUint16(data + offset + 2);
        const std::uint16_t data_length = ReadUint16(data + offset + 8);
        offset += 10;
        if (offset + data_length > size) {
            return WSATRY_AGAIN;
        }

        if (type == query_type && record_class == 1 &&
            ((type == kTypeA && data_length == 4) ||
             (type == kTypeAaaa && data_length == 16))) {
            Address address;
            address.family = type == kTypeA ? AF_INET : AF_INET6;
            std::memcpy(address.bytes.data(), data + offset, data_length);
            if (std::find(output.begin(), output.end(), address) == output.end()) {
                output.push_back(address);
            }
        }
        offset += data_length;
    }
    return output.empty() ? WSANO_DATA : 0;
}

int Resolve(const Endpoint& server,
            std::string_view hostname,
            int family,
            const SocketApi& socket_api,
            std::vector<Address>& output) {
    output.clear();
    if (socket_api.connect_socket == nullptr || socket_api.send_data == nullptr ||
        socket_api.receive_data == nullptr || socket_api.send_to == nullptr ||
        socket_api.receive_from == nullptr || socket_api.close_socket == nullptr) {
        return WSAEINVAL;
    }

    std::array<std::uint16_t, 2> types{};
    std::size_t type_count = 0;
    if (family == AF_UNSPEC || family == AF_INET) {
        types[type_count++] = kTypeA;
    }
    if (family == AF_UNSPEC || family == AF_INET6) {
        types[type_count++] = kTypeAaaa;
    }
    if (type_count == 0) {
        return WSAEAFNOSUPPORT;
    }

    int last_error = WSAHOST_NOT_FOUND;
    for (std::size_t index = 0; index < type_count; ++index) {
        std::vector<Address> query_result;
        const int error = QueryOnce(server, socket_api, hostname, types[index], query_result);
        if (error == 0) {
            for (const Address& address : query_result) {
                if (std::find(output.begin(), output.end(), address) == output.end()) {
                    output.push_back(address);
                }
            }
        } else if (error != WSANO_DATA) {
            last_error = error;
        }
    }
    return output.empty() ? last_error : 0;
}

}  // namespace easy_net::dns
