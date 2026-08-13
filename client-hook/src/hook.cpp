#include <winsock2.h>
#include <ws2tcpip.h>
#include <mswsock.h>
#include <windows.h>
#include <detours.h>
#include <tlhelp32.h>
#include <process.h>

#include <algorithm>
#include <array>
#include <atomic>
#include <cstdint>
#include <cstdlib>
#include <cwchar>
#include <cstring>
#include <iterator>
#include <memory>
#include <new>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <vector>

#include "child_injection.h"
#include "config_ipc.h"
#include "dns_resolver.h"
#include "socks5_protocol.h"

namespace {

struct Config {
    bool enabled = false;
    bool valid = false;
    bool inject_children = true;
    bool allow_udp_direct = false;
    bool custom_dns = false;
    bool dns_valid = true;
    sockaddr_storage proxy{};
    int proxy_length = 0;
    easy_net::dns::Endpoint dns_server{};
    std::string username;
    std::string password;
};

INIT_ONCE g_config_once = INIT_ONCE_STATIC_INIT;
Config g_config;
char g_dll_path[MAX_PATH]{};
SRWLOCK g_socket_state_lock = SRWLOCK_INIT;
std::unordered_set<SOCKET> g_nonblocking_sockets;
struct RelayContext {
    std::atomic<long> references{2};
    std::atomic<SOCKET> listener{INVALID_SOCKET};
    sockaddr_storage destination{};
    int destination_length = 0;
};

void ReleaseRelayContext(RelayContext* context);
void CloseRelayListener(RelayContext* context);

struct ProxiedPeer {
    sockaddr_storage original{};
    int original_length = 0;
    sockaddr_storage relay{};
    int relay_length = 0;
    RelayContext* relay_context = nullptr;
};
SRWLOCK g_proxied_peer_lock = SRWLOCK_INIT;
std::unordered_map<SOCKET, ProxiedPeer> g_proxied_peers;
SRWLOCK g_address_info_lock = SRWLOCK_INIT;
std::unordered_set<PADDRINFOA> g_custom_address_info_a;
std::unordered_set<PADDRINFOW> g_custom_address_info_w;
PVOID volatile g_fallback_connect_ex = nullptr;
PVOID volatile g_fallback_wsa_send_msg = nullptr;
LPFN_CONNECTEX RealConnectEx = nullptr;
LPFN_WSASENDMSG RealWSASendMsg = nullptr;
std::atomic<bool> g_connect_ex_hook_attached{false};
std::atomic<bool> g_wsa_send_msg_hook_attached{false};

decltype(&connect) RealConnect = connect;
decltype(&WSAConnect) RealWSAConnect = WSAConnect;
decltype(&sendto) RealSendTo = sendto;
decltype(&WSASendTo) RealWSASendTo = WSASendTo;
decltype(&send) RealSend = send;
decltype(&WSASend) RealWSASend = WSASend;
decltype(&recv) RealRecv = recv;
decltype(&recvfrom) RealRecvFrom = recvfrom;
decltype(&ioctlsocket) RealIoctlSocket = ioctlsocket;
decltype(&WSAEventSelect) RealWSAEventSelect = WSAEventSelect;
decltype(&WSAAsyncSelect) RealWSAAsyncSelect = WSAAsyncSelect;
decltype(&WSAIoctl) RealWSAIoctl = WSAIoctl;
decltype(&closesocket) RealCloseSocket = closesocket;
decltype(&getpeername) RealGetPeerName = getpeername;
decltype(&getaddrinfo) RealGetAddrInfoA = getaddrinfo;
decltype(&freeaddrinfo) RealFreeAddrInfoA = freeaddrinfo;
decltype(&GetAddrInfoW) RealGetAddrInfoW = GetAddrInfoW;
decltype(&FreeAddrInfoW) RealFreeAddrInfoW = FreeAddrInfoW;
decltype(&GetAddrInfoExA) RealGetAddrInfoExA = GetAddrInfoExA;
decltype(&GetAddrInfoExW) RealGetAddrInfoExW = GetAddrInfoExW;
decltype(&CreateProcessW) RealCreateProcessW = CreateProcessW;
decltype(&CreateProcessA) RealCreateProcessA = CreateProcessA;

std::wstring ReadEnvironment(const wchar_t* name) {
    const DWORD required = GetEnvironmentVariableW(name, nullptr, 0);
    if (required == 0) {
        return {};
    }
    std::wstring value(static_cast<std::size_t>(required), L'\0');
    const DWORD written = GetEnvironmentVariableW(name, value.data(), required);
    if (written == 0 || written >= required) {
        return {};
    }
    value.resize(written);
    return value;
}

std::string ToUtf8(const std::wstring& value) {
    if (value.empty()) {
        return {};
    }
    const int required = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
                                             static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
    if (required <= 0) {
        return {};
    }
    std::string result(static_cast<std::size_t>(required), '\0');
    if (WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
                            static_cast<int>(value.size()), result.data(), required,
                            nullptr, nullptr) != required) {
        return {};
    }
    return result;
}

bool ParseBoolean(const wchar_t* name, bool fallback) {
    const std::wstring value = ReadEnvironment(name);
    if (value.empty()) {
        return fallback;
    }
    return value != L"0" && value != L"false" && value != L"FALSE";
}

bool ReadMappedConfig(easy_net::ipc::ConfigBlock& block) {
    const std::wstring name = easy_net::ipc::ConfigMappingName(GetCurrentProcessId());
    const HANDLE mapping = OpenFileMappingW(FILE_MAP_READ, FALSE, name.c_str());
    if (mapping == nullptr) {
        return false;
    }
    const void* view = MapViewOfFile(mapping, FILE_MAP_READ, 0, 0, sizeof(block));
    if (view == nullptr) {
        CloseHandle(mapping);
        return false;
    }
    std::memcpy(&block, view, sizeof(block));
    UnmapViewOfFile(view);
    CloseHandle(mapping);
    return easy_net::ipc::IsValid(block);
}

void PublishAttachedEnvironment(const easy_net::ipc::ConfigBlock& block) {
    SetEnvironmentVariableW(L"EASY_NET_HOOK_PROXY", block.proxy);
    SetEnvironmentVariableW(L"EASY_NET_HOOK_USERNAME", block.username);
    SetEnvironmentVariableW(L"EASY_NET_HOOK_PASSWORD", block.password);
    SetEnvironmentVariableW(L"EASY_NET_HOOK_DNS", block.dns);
    SetEnvironmentVariableW(L"EASY_NET_HOOK_CHILDREN", block.inject_children != 0 ? L"1" : L"0");
    SetEnvironmentVariableW(L"EASY_NET_HOOK_ALLOW_UDP_DIRECT",
                            block.allow_udp_direct != 0 ? L"1" : L"0");
}

bool ParseProxyAddress(const std::wstring& value, sockaddr_storage& output, int& output_length) {
    std::wstring host;
    std::wstring port_text;
    if (!value.empty() && value.front() == L'[') {
        const std::size_t close = value.find(L']');
        if (close == std::wstring::npos || close + 1 >= value.size() || value[close + 1] != L':') {
            return false;
        }
        host = value.substr(1, close - 1);
        port_text = value.substr(close + 2);
    } else {
        const std::size_t colon = value.rfind(L':');
        if (colon == std::wstring::npos) {
            return false;
        }
        host = value.substr(0, colon);
        port_text = value.substr(colon + 1);
    }

    wchar_t* end = nullptr;
    const long port = std::wcstol(port_text.c_str(), &end, 10);
    if (port <= 0 || port > 65535 || end == nullptr || *end != L'\0') {
        return false;
    }

    sockaddr_in ipv4{};
    ipv4.sin_family = AF_INET;
    ipv4.sin_port = htons(static_cast<u_short>(port));
    if (InetPtonW(AF_INET, host.c_str(), &ipv4.sin_addr) == 1) {
        std::memcpy(&output, &ipv4, sizeof(ipv4));
        output_length = sizeof(ipv4);
        return true;
    }

    sockaddr_in6 ipv6{};
    ipv6.sin6_family = AF_INET6;
    ipv6.sin6_port = htons(static_cast<u_short>(port));
    if (InetPtonW(AF_INET6, host.c_str(), &ipv6.sin6_addr) == 1) {
        std::memcpy(&output, &ipv6, sizeof(ipv6));
        output_length = sizeof(ipv6);
        return true;
    }
    return false;
}

BOOL CALLBACK InitializeConfig(PINIT_ONCE, PVOID, PVOID*) {
    easy_net::ipc::ConfigBlock attached;
    const bool attached_process = ReadMappedConfig(attached);
    if (attached_process) {
        PublishAttachedEnvironment(attached);
    }

    const std::wstring proxy = attached_process ? attached.proxy
                                                : ReadEnvironment(L"EASY_NET_HOOK_PROXY");
    if (proxy.empty()) {
        return TRUE;
    }

    g_config.enabled = true;
    g_config.inject_children = attached_process ? attached.inject_children != 0
                                                : ParseBoolean(L"EASY_NET_HOOK_CHILDREN", true);
    g_config.allow_udp_direct = attached_process ? attached.allow_udp_direct != 0
                                                 : ParseBoolean(L"EASY_NET_HOOK_ALLOW_UDP_DIRECT", false);
    const std::wstring dns_server = attached_process ? attached.dns
                                                     : ReadEnvironment(L"EASY_NET_HOOK_DNS");
    g_config.custom_dns = !dns_server.empty();
    g_config.dns_valid = !g_config.custom_dns ||
                         easy_net::dns::ParseEndpoint(dns_server, g_config.dns_server);
    g_config.username = ToUtf8(attached_process ? std::wstring(attached.username)
                                                : ReadEnvironment(L"EASY_NET_HOOK_USERNAME"));
    g_config.password = ToUtf8(attached_process ? std::wstring(attached.password)
                                                : ReadEnvironment(L"EASY_NET_HOOK_PASSWORD"));
    g_config.valid = ParseProxyAddress(proxy, g_config.proxy, g_config.proxy_length) &&
                     g_config.username.size() <= 255 && g_config.password.size() <= 255;
    if (!g_config.valid) {
        OutputDebugStringW(L"[Easy-Net Hook] Invalid proxy configuration; connections will fail closed.\n");
    }
    if (!g_config.dns_valid) {
        OutputDebugStringW(L"[Easy-Net Hook] Invalid DNS configuration; name resolution will fail closed.\n");
    }
    return TRUE;
}

const Config& GetConfig() {
    InitOnceExecuteOnce(&g_config_once, InitializeConfig, nullptr, nullptr);
    return g_config;
}

struct SocketVariant {
    int socket_type = 0;
    int protocol = 0;
};

bool IsNumericHost(const char* hostname) {
    in_addr ipv4{};
    in6_addr ipv6{};
    return hostname != nullptr &&
           (InetPtonA(AF_INET, hostname, &ipv4) == 1 || InetPtonA(AF_INET6, hostname, &ipv6) == 1);
}

bool IsNumericHost(const wchar_t* hostname) {
    in_addr ipv4{};
    in6_addr ipv6{};
    return hostname != nullptr &&
           (InetPtonW(AF_INET, hostname, &ipv4) == 1 || InetPtonW(AF_INET6, hostname, &ipv6) == 1);
}

bool IsLocalhost(const char* hostname) {
    return hostname != nullptr &&
           (_stricmp(hostname, "localhost") == 0 || _stricmp(hostname, "localhost.") == 0);
}

bool IsLocalhost(const wchar_t* hostname) {
    return hostname != nullptr &&
           (_wcsicmp(hostname, L"localhost") == 0 || _wcsicmp(hostname, L"localhost.") == 0);
}

bool WideHostnameToAscii(const wchar_t* hostname, std::string& output) {
    output.clear();
    if (hostname == nullptr || *hostname == L'\0') {
        return false;
    }
    const int required = IdnToAscii(IDN_USE_STD3_ASCII_RULES, hostname, -1, nullptr, 0);
    if (required <= 1) {
        return false;
    }
    std::wstring ascii(static_cast<std::size_t>(required), L'\0');
    if (IdnToAscii(IDN_USE_STD3_ASCII_RULES, hostname, -1, ascii.data(), required) != required) {
        return false;
    }
    if (!ascii.empty() && ascii.back() == L'\0') {
        ascii.pop_back();
    }
    output = ToUtf8(ascii);
    return !output.empty();
}

bool WideServiceToAscii(const wchar_t* service, std::string& output) {
    output.clear();
    if (service == nullptr) {
        return true;
    }
    for (const wchar_t* cursor = service; *cursor != L'\0'; ++cursor) {
        if (static_cast<unsigned int>(*cursor) > 0x7fU) {
            return false;
        }
        output.push_back(static_cast<char>(*cursor));
    }
    return true;
}

int BuildSocketVariants(int socket_type, int protocol, std::vector<SocketVariant>& variants) {
    variants.clear();
    if (socket_type == 0 && protocol == 0) {
        variants.push_back({SOCK_STREAM, IPPROTO_TCP});
        variants.push_back({SOCK_DGRAM, IPPROTO_UDP});
        return 0;
    }
    if (socket_type == 0) {
        if (protocol == IPPROTO_TCP) {
            variants.push_back({SOCK_STREAM, IPPROTO_TCP});
        } else if (protocol == IPPROTO_UDP) {
            variants.push_back({SOCK_DGRAM, IPPROTO_UDP});
        } else {
            return WSAESOCKTNOSUPPORT;
        }
        return 0;
    }
    if (socket_type != SOCK_STREAM && socket_type != SOCK_DGRAM) {
        return WSAESOCKTNOSUPPORT;
    }
    const int inferred_protocol = socket_type == SOCK_STREAM ? IPPROTO_TCP : IPPROTO_UDP;
    if (protocol != 0 && protocol != inferred_protocol) {
        return WSAEPROTONOSUPPORT;
    }
    variants.push_back({socket_type, protocol == 0 ? inferred_protocol : protocol});
    return 0;
}

int ResolveServicePort(const char* service,
                       const std::vector<SocketVariant>& variants,
                       bool numeric_only,
                       std::uint16_t& port) {
    port = 0;
    if (service == nullptr || *service == '\0') {
        return 0;
    }
    if (numeric_only) {
        return EAI_NONAME;
    }
    char* end = nullptr;
    const long numeric_port = std::strtol(service, &end, 10);
    if (end != service && end != nullptr && *end == '\0') {
        if (numeric_port < 0 || numeric_port > 65535) {
            return WSAEINVAL;
        }
        port = static_cast<std::uint16_t>(numeric_port);
        return 0;
    }

    const char* protocol = variants.empty() || variants.front().protocol == IPPROTO_TCP ? "tcp" : "udp";
    const servent* entry = getservbyname(service, protocol);
    if (entry == nullptr) {
        return WSATYPE_NOT_FOUND;
    }
    port = ntohs(static_cast<u_short>(entry->s_port));
    return 0;
}

easy_net::dns::SocketApi DnsSocketApi() {
    return {
        RealConnect,
        RealSend,
        RealRecv,
        RealSendTo,
        RealRecvFrom,
        RealCloseSocket,
    };
}

void FreeCustomAddressInfo(PADDRINFOA head) {
    while (head != nullptr) {
        PADDRINFOA next = head->ai_next;
        if (head->ai_addr != nullptr) {
            HeapFree(GetProcessHeap(), 0, head->ai_addr);
        }
        if (head->ai_canonname != nullptr) {
            HeapFree(GetProcessHeap(), 0, head->ai_canonname);
        }
        HeapFree(GetProcessHeap(), 0, head);
        head = next;
    }
}

void FreeCustomAddressInfo(PADDRINFOW head) {
    while (head != nullptr) {
        PADDRINFOW next = head->ai_next;
        if (head->ai_addr != nullptr) {
            HeapFree(GetProcessHeap(), 0, head->ai_addr);
        }
        if (head->ai_canonname != nullptr) {
            HeapFree(GetProcessHeap(), 0, head->ai_canonname);
        }
        HeapFree(GetProcessHeap(), 0, head);
        head = next;
    }
}

char* DuplicateCanonicalName(const char* hostname) {
    const std::size_t size = std::strlen(hostname) + 1;
    auto* copy = static_cast<char*>(HeapAlloc(GetProcessHeap(), 0, size));
    if (copy != nullptr) {
        std::memcpy(copy, hostname, size);
    }
    return copy;
}

wchar_t* DuplicateCanonicalName(const wchar_t* hostname) {
    const std::size_t size = (std::wcslen(hostname) + 1) * sizeof(wchar_t);
    auto* copy = static_cast<wchar_t*>(HeapAlloc(GetProcessHeap(), 0, size));
    if (copy != nullptr) {
        std::memcpy(copy, hostname, size);
    }
    return copy;
}

int BuildAddressInfoA(const char* hostname,
                      std::uint16_t port,
                      int flags,
                      const std::vector<SocketVariant>& variants,
                      const std::vector<easy_net::dns::Address>& addresses,
                      PADDRINFOA* result) {
    PADDRINFOA head = nullptr;
    PADDRINFOA* next = &head;
    bool first = true;
    for (const auto& address : addresses) {
        for (const SocketVariant& variant : variants) {
            auto* node = static_cast<PADDRINFOA>(
                HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof(ADDRINFOA)));
            if (node == nullptr) {
                FreeCustomAddressInfo(head);
                return EAI_MEMORY;
            }
            node->ai_flags = flags;
            node->ai_family = address.family;
            node->ai_socktype = variant.socket_type;
            node->ai_protocol = variant.protocol;
            if (address.family == AF_INET) {
                auto* socket_address = static_cast<sockaddr_in*>(
                    HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof(sockaddr_in)));
                if (socket_address != nullptr) {
                    socket_address->sin_family = AF_INET;
                    socket_address->sin_port = htons(port);
                    std::memcpy(&socket_address->sin_addr, address.bytes.data(), 4);
                    node->ai_addr = reinterpret_cast<sockaddr*>(socket_address);
                    node->ai_addrlen = sizeof(sockaddr_in);
                }
            } else {
                auto* socket_address = static_cast<sockaddr_in6*>(
                    HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof(sockaddr_in6)));
                if (socket_address != nullptr) {
                    socket_address->sin6_family = AF_INET6;
                    socket_address->sin6_port = htons(port);
                    std::memcpy(&socket_address->sin6_addr, address.bytes.data(), 16);
                    node->ai_addr = reinterpret_cast<sockaddr*>(socket_address);
                    node->ai_addrlen = sizeof(sockaddr_in6);
                }
            }
            if (node->ai_addr == nullptr) {
                HeapFree(GetProcessHeap(), 0, node);
                FreeCustomAddressInfo(head);
                return EAI_MEMORY;
            }
            if (first && (flags & AI_CANONNAME) != 0) {
                node->ai_canonname = DuplicateCanonicalName(hostname);
                if (node->ai_canonname == nullptr) {
                    FreeCustomAddressInfo(node);
                    FreeCustomAddressInfo(head);
                    return EAI_MEMORY;
                }
            }
            *next = node;
            next = &node->ai_next;
            first = false;
        }
    }
    if (head == nullptr) {
        return EAI_NONAME;
    }
    AcquireSRWLockExclusive(&g_address_info_lock);
    g_custom_address_info_a.insert(head);
    ReleaseSRWLockExclusive(&g_address_info_lock);
    *result = head;
    return 0;
}

int BuildAddressInfoW(const wchar_t* hostname,
                      std::uint16_t port,
                      int flags,
                      const std::vector<SocketVariant>& variants,
                      const std::vector<easy_net::dns::Address>& addresses,
                      PADDRINFOW* result) {
    PADDRINFOW head = nullptr;
    PADDRINFOW* next = &head;
    bool first = true;
    for (const auto& address : addresses) {
        for (const SocketVariant& variant : variants) {
            auto* node = static_cast<PADDRINFOW>(
                HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof(ADDRINFOW)));
            if (node == nullptr) {
                FreeCustomAddressInfo(head);
                return EAI_MEMORY;
            }
            node->ai_flags = flags;
            node->ai_family = address.family;
            node->ai_socktype = variant.socket_type;
            node->ai_protocol = variant.protocol;
            if (address.family == AF_INET) {
                auto* socket_address = static_cast<sockaddr_in*>(
                    HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof(sockaddr_in)));
                if (socket_address != nullptr) {
                    socket_address->sin_family = AF_INET;
                    socket_address->sin_port = htons(port);
                    std::memcpy(&socket_address->sin_addr, address.bytes.data(), 4);
                    node->ai_addr = reinterpret_cast<sockaddr*>(socket_address);
                    node->ai_addrlen = sizeof(sockaddr_in);
                }
            } else {
                auto* socket_address = static_cast<sockaddr_in6*>(
                    HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof(sockaddr_in6)));
                if (socket_address != nullptr) {
                    socket_address->sin6_family = AF_INET6;
                    socket_address->sin6_port = htons(port);
                    std::memcpy(&socket_address->sin6_addr, address.bytes.data(), 16);
                    node->ai_addr = reinterpret_cast<sockaddr*>(socket_address);
                    node->ai_addrlen = sizeof(sockaddr_in6);
                }
            }
            if (node->ai_addr == nullptr) {
                HeapFree(GetProcessHeap(), 0, node);
                FreeCustomAddressInfo(head);
                return EAI_MEMORY;
            }
            if (first && (flags & AI_CANONNAME) != 0) {
                node->ai_canonname = DuplicateCanonicalName(hostname);
                if (node->ai_canonname == nullptr) {
                    FreeCustomAddressInfo(node);
                    FreeCustomAddressInfo(head);
                    return EAI_MEMORY;
                }
            }
            *next = node;
            next = &node->ai_next;
            first = false;
        }
    }
    if (head == nullptr) {
        return EAI_NONAME;
    }
    AcquireSRWLockExclusive(&g_address_info_lock);
    g_custom_address_info_w.insert(head);
    ReleaseSRWLockExclusive(&g_address_info_lock);
    *result = head;
    return 0;
}

int ResolveWithCustomDnsA(const char* hostname,
                          const char* service,
                          const ADDRINFOA* hints,
                          PADDRINFOA* result) {
    if (result == nullptr) {
        return WSAEFAULT;
    }
    *result = nullptr;
    if (hints != nullptr && (hints->ai_addrlen != 0 || hints->ai_canonname != nullptr ||
                             hints->ai_addr != nullptr || hints->ai_next != nullptr)) {
        return WSANO_RECOVERY;
    }
    const int family = hints == nullptr ? AF_UNSPEC : hints->ai_family;
    const int flags = hints == nullptr ? 0 : hints->ai_flags;
    std::vector<SocketVariant> variants;
    int error = BuildSocketVariants(hints == nullptr ? 0 : hints->ai_socktype,
                                    hints == nullptr ? 0 : hints->ai_protocol, variants);
    if (error != 0) {
        return error;
    }
    std::uint16_t port = 0;
    error = ResolveServicePort(service, variants, (flags & AI_NUMERICSERV) != 0, port);
    if (error != 0) {
        return error;
    }
    std::vector<easy_net::dns::Address> addresses;
    error = easy_net::dns::Resolve(GetConfig().dns_server, hostname, family,
                                   DnsSocketApi(), addresses);
    if (error != 0) {
        return error;
    }
    return BuildAddressInfoA(hostname, port, flags, variants, addresses, result);
}

int ResolveWithCustomDnsW(const wchar_t* hostname,
                          const wchar_t* service,
                          const ADDRINFOW* hints,
                          PADDRINFOW* result) {
    if (result == nullptr) {
        return WSAEFAULT;
    }
    *result = nullptr;
    if (hints != nullptr && (hints->ai_addrlen != 0 || hints->ai_canonname != nullptr ||
                             hints->ai_addr != nullptr || hints->ai_next != nullptr)) {
        return WSANO_RECOVERY;
    }
    std::string ascii_hostname;
    std::string ascii_service;
    if (!WideHostnameToAscii(hostname, ascii_hostname) ||
        !WideServiceToAscii(service, ascii_service)) {
        return EAI_NONAME;
    }
    const int family = hints == nullptr ? AF_UNSPEC : hints->ai_family;
    const int flags = hints == nullptr ? 0 : hints->ai_flags;
    std::vector<SocketVariant> variants;
    int error = BuildSocketVariants(hints == nullptr ? 0 : hints->ai_socktype,
                                    hints == nullptr ? 0 : hints->ai_protocol, variants);
    if (error != 0) {
        return error;
    }
    std::uint16_t port = 0;
    error = ResolveServicePort(service == nullptr ? nullptr : ascii_service.c_str(), variants,
                               (flags & AI_NUMERICSERV) != 0, port);
    if (error != 0) {
        return error;
    }
    std::vector<easy_net::dns::Address> addresses;
    error = easy_net::dns::Resolve(GetConfig().dns_server, ascii_hostname, family,
                                   DnsSocketApi(), addresses);
    if (error != 0) {
        return error;
    }
    return BuildAddressInfoW(hostname, port, flags, variants, addresses, result);
}

int WSAAPI HookedGetAddrInfoA(PCSTR hostname,
                              PCSTR service,
                              const ADDRINFOA* hints,
                              PADDRINFOA* result) {
    const Config& config = GetConfig();
    if (!config.custom_dns || hostname == nullptr || IsNumericHost(hostname) ||
        IsLocalhost(hostname) || (hints != nullptr && (hints->ai_flags & AI_NUMERICHOST) != 0)) {
        return RealGetAddrInfoA(hostname, service, hints, result);
    }
    if (!config.dns_valid) {
        WSASetLastError(WSAEINVAL);
        return WSAEINVAL;
    }
    const int error = ResolveWithCustomDnsA(hostname, service, hints, result);
    if (error != 0) {
        WSASetLastError(error);
    }
    return error;
}

INT WSAAPI HookedGetAddrInfoW(PCWSTR hostname,
                              PCWSTR service,
                              const ADDRINFOW* hints,
                              PADDRINFOW* result) {
    const Config& config = GetConfig();
    if (!config.custom_dns || hostname == nullptr || IsNumericHost(hostname) ||
        IsLocalhost(hostname) || (hints != nullptr && (hints->ai_flags & AI_NUMERICHOST) != 0)) {
        return RealGetAddrInfoW(hostname, service, hints, result);
    }
    if (!config.dns_valid) {
        WSASetLastError(WSAEINVAL);
        return WSAEINVAL;
    }
    const int error = ResolveWithCustomDnsW(hostname, service, hints, result);
    if (error != 0) {
        WSASetLastError(error);
    }
    return error;
}

void WSAAPI HookedFreeAddrInfoA(PADDRINFOA address_info) {
    bool custom = false;
    AcquireSRWLockExclusive(&g_address_info_lock);
    const auto iterator = g_custom_address_info_a.find(address_info);
    if (iterator != g_custom_address_info_a.end()) {
        g_custom_address_info_a.erase(iterator);
        custom = true;
    }
    ReleaseSRWLockExclusive(&g_address_info_lock);
    if (custom) {
        FreeCustomAddressInfo(address_info);
    } else {
        RealFreeAddrInfoA(address_info);
    }
}

VOID WSAAPI HookedFreeAddrInfoW(PADDRINFOW address_info) {
    bool custom = false;
    AcquireSRWLockExclusive(&g_address_info_lock);
    const auto iterator = g_custom_address_info_w.find(address_info);
    if (iterator != g_custom_address_info_w.end()) {
        g_custom_address_info_w.erase(iterator);
        custom = true;
    }
    ReleaseSRWLockExclusive(&g_address_info_lock);
    if (custom) {
        FreeCustomAddressInfo(address_info);
    } else {
        RealFreeAddrInfoW(address_info);
    }
}

INT WSAAPI HookedGetAddrInfoExA(PCSTR hostname,
                                PCSTR service,
                                DWORD name_space,
                                LPGUID namespace_provider,
                                const ADDRINFOEXA* hints,
                                PADDRINFOEXA* result,
                                timeval* timeout,
                                LPOVERLAPPED overlapped,
                                LPLOOKUPSERVICE_COMPLETION_ROUTINE completion,
                                LPHANDLE cancel_handle) {
    if (GetConfig().custom_dns && hostname != nullptr && !IsNumericHost(hostname) &&
        !IsLocalhost(hostname)) {
        WSASetLastError(WSAEOPNOTSUPP);
        return WSAEOPNOTSUPP;
    }
    return RealGetAddrInfoExA(hostname, service, name_space, namespace_provider, hints,
                              result, timeout, overlapped, completion, cancel_handle);
}

INT WSAAPI HookedGetAddrInfoExW(PCWSTR hostname,
                                PCWSTR service,
                                DWORD name_space,
                                LPGUID namespace_provider,
                                const ADDRINFOEXW* hints,
                                PADDRINFOEXW* result,
                                timeval* timeout,
                                LPOVERLAPPED overlapped,
                                LPLOOKUPSERVICE_COMPLETION_ROUTINE completion,
                                LPHANDLE cancel_handle) {
    if (GetConfig().custom_dns && hostname != nullptr && !IsNumericHost(hostname) &&
        !IsLocalhost(hostname)) {
        WSASetLastError(WSAEOPNOTSUPP);
        return WSAEOPNOTSUPP;
    }
    return RealGetAddrInfoExW(hostname, service, name_space, namespace_provider, hints,
                              result, timeout, overlapped, completion, cancel_handle);
}

bool IsSocketNonblocking(SOCKET socket) {
    AcquireSRWLockShared(&g_socket_state_lock);
    const bool found = g_nonblocking_sockets.contains(socket);
    ReleaseSRWLockShared(&g_socket_state_lock);
    return found;
}

void SetSocketNonblocking(SOCKET socket, bool nonblocking) {
    AcquireSRWLockExclusive(&g_socket_state_lock);
    if (nonblocking) {
        g_nonblocking_sockets.insert(socket);
    } else {
        g_nonblocking_sockets.erase(socket);
    }
    ReleaseSRWLockExclusive(&g_socket_state_lock);
}

bool IsLoopback(const sockaddr* address, int length) {
    if (address == nullptr) {
        return false;
    }
    if (address->sa_family == AF_INET && length >= static_cast<int>(sizeof(sockaddr_in))) {
        const auto* ipv4 = reinterpret_cast<const sockaddr_in*>(address);
        return (ntohl(ipv4->sin_addr.s_addr) >> 24U) == 127U;
    }
    if (address->sa_family == AF_INET6 && length >= static_cast<int>(sizeof(sockaddr_in6))) {
        const auto* ipv6 = reinterpret_cast<const sockaddr_in6*>(address);
        return IN6_IS_ADDR_LOOPBACK(&ipv6->sin6_addr) != 0;
    }
    return false;
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
        return a->sin6_port == b->sin6_port &&
               std::memcmp(&a->sin6_addr, &b->sin6_addr, sizeof(in6_addr)) == 0;
    }
    return false;
}

int SocketType(SOCKET socket) {
    int type = 0;
    int length = sizeof(type);
    if (getsockopt(socket, SOL_SOCKET, SO_TYPE, reinterpret_cast<char*>(&type), &length) != 0) {
        return 0;
    }
    return type;
}

bool MakeProxyEndpoint(int socket_family, sockaddr_storage& output, int& output_length) {
    const Config& config = GetConfig();
    const auto* proxy = reinterpret_cast<const sockaddr*>(&config.proxy);
    if (proxy->sa_family == socket_family) {
        output = config.proxy;
        output_length = config.proxy_length;
        return true;
    }
    if (proxy->sa_family == AF_INET && socket_family == AF_INET6) {
        const auto* ipv4 = reinterpret_cast<const sockaddr_in*>(proxy);
        sockaddr_in6 mapped{};
        mapped.sin6_family = AF_INET6;
        mapped.sin6_port = ipv4->sin_port;
        mapped.sin6_addr.u.Byte[10] = 0xff;
        mapped.sin6_addr.u.Byte[11] = 0xff;
        std::memcpy(&mapped.sin6_addr.u.Byte[12], &ipv4->sin_addr, sizeof(ipv4->sin_addr));
        std::memcpy(&output, &mapped, sizeof(mapped));
        output_length = sizeof(mapped);
        return true;
    }
    return false;
}

bool SendAll(SOCKET socket, const std::uint8_t* data, std::size_t size) {
    std::size_t sent = 0;
    while (sent < size) {
        const int chunk = RealSend(socket, reinterpret_cast<const char*>(data + sent),
                                   static_cast<int>(size - sent), 0);
        if (chunk == SOCKET_ERROR || chunk == 0) {
            return false;
        }
        sent += static_cast<std::size_t>(chunk);
    }
    return true;
}

bool ReceiveExact(SOCKET socket, std::uint8_t* data, std::size_t size) {
    std::size_t received = 0;
    while (received < size) {
        const int chunk = RealRecv(socket, reinterpret_cast<char*>(data + received),
                                   static_cast<int>(size - received), 0);
        if (chunk == SOCKET_ERROR || chunk == 0) {
            return false;
        }
        received += static_cast<std::size_t>(chunk);
    }
    return true;
}

int NegotiateSocks5(SOCKET socket, const sockaddr* destination, int destination_length) {
    const Config& config = GetConfig();
    const bool authenticate = !config.username.empty() || !config.password.empty();
    const std::array<std::uint8_t, 3> greeting{
        0x05, 0x01, static_cast<std::uint8_t>(authenticate ? 0x02 : 0x00)};
    if (!SendAll(socket, greeting.data(), greeting.size())) {
        return WSAECONNABORTED;
    }

    std::array<std::uint8_t, 2> selection{};
    if (!ReceiveExact(socket, selection.data(), selection.size()) || selection[0] != 0x05 ||
        selection[1] == 0xff || selection[1] != greeting[2]) {
        return WSAEACCES;
    }

    if (authenticate) {
        std::vector<std::uint8_t> auth;
        auth.reserve(3 + config.username.size() + config.password.size());
        auth.push_back(0x01);
        auth.push_back(static_cast<std::uint8_t>(config.username.size()));
        auth.insert(auth.end(), config.username.begin(), config.username.end());
        auth.push_back(static_cast<std::uint8_t>(config.password.size()));
        auth.insert(auth.end(), config.password.begin(), config.password.end());
        std::array<std::uint8_t, 2> auth_reply{};
        if (!SendAll(socket, auth.data(), auth.size()) ||
            !ReceiveExact(socket, auth_reply.data(), auth_reply.size()) ||
            auth_reply[0] != 0x01 || auth_reply[1] != 0x00) {
            return WSAEACCES;
        }
    }

    std::vector<std::uint8_t> request;
    if (!easy_net::socks5::BuildConnectRequest(destination, destination_length, request)) {
        return WSAEAFNOSUPPORT;
    }
    if (!SendAll(socket, request.data(), request.size())) {
        return WSAECONNABORTED;
    }

    std::array<std::uint8_t, 4> reply{};
    if (!ReceiveExact(socket, reply.data(), reply.size()) || reply[0] != 0x05) {
        return WSAECONNABORTED;
    }
    if (reply[1] != 0x00) {
        return easy_net::socks5::ReplyToWsaError(reply[1]);
    }

    std::size_t remaining = 0;
    if (reply[3] == 0x01) {
        remaining = 4 + 2;
    } else if (reply[3] == 0x04) {
        remaining = 16 + 2;
    } else if (reply[3] == 0x03) {
        std::uint8_t domain_length = 0;
        if (!ReceiveExact(socket, &domain_length, 1)) {
            return WSAECONNABORTED;
        }
        remaining = static_cast<std::size_t>(domain_length) + 2;
    } else {
        return WSAECONNABORTED;
    }
    std::vector<std::uint8_t> ignored(remaining);
    return ReceiveExact(socket, ignored.data(), ignored.size()) ? 0 : WSAECONNABORTED;
}

void RememberProxiedPeer(SOCKET socket,
                         const sockaddr* original,
                         int original_length,
                         const sockaddr* relay,
                         int relay_length,
                         RelayContext* relay_context) {
    ProxiedPeer peer;
    peer.original_length = (std::min)(original_length, static_cast<int>(sizeof(peer.original)));
    peer.relay_length = (std::min)(relay_length, static_cast<int>(sizeof(peer.relay)));
    std::memcpy(&peer.original, original, static_cast<std::size_t>(peer.original_length));
    std::memcpy(&peer.relay, relay, static_cast<std::size_t>(peer.relay_length));
    peer.relay_context = relay_context;
    if (relay_context != nullptr) {
        relay_context->references.fetch_add(1);
    }
    AcquireSRWLockExclusive(&g_proxied_peer_lock);
    const auto existing = g_proxied_peers.find(socket);
    RelayContext* previous_context = nullptr;
    if (existing != g_proxied_peers.end()) {
        previous_context = existing->second.relay_context;
        existing->second = peer;
    } else {
        g_proxied_peers.emplace(socket, peer);
    }
    ReleaseSRWLockExclusive(&g_proxied_peer_lock);
    if (previous_context != nullptr) {
        CloseRelayListener(previous_context);
        ReleaseRelayContext(previous_context);
    }
}

bool FindProxiedPeer(SOCKET socket, ProxiedPeer& peer) {
    AcquireSRWLockShared(&g_proxied_peer_lock);
    const auto iterator = g_proxied_peers.find(socket);
    const bool found = iterator != g_proxied_peers.end();
    if (found) {
        peer = iterator->second;
    }
    ReleaseSRWLockShared(&g_proxied_peer_lock);
    return found;
}

void ForgetProxiedPeer(SOCKET socket) {
    RelayContext* context = nullptr;
    AcquireSRWLockExclusive(&g_proxied_peer_lock);
    const auto iterator = g_proxied_peers.find(socket);
    if (iterator != g_proxied_peers.end()) {
        context = iterator->second.relay_context;
        g_proxied_peers.erase(iterator);
    }
    ReleaseSRWLockExclusive(&g_proxied_peer_lock);
    if (context != nullptr) {
        CloseRelayListener(context);
        ReleaseRelayContext(context);
    }
}

struct RelayTicket {
    RelayContext* context = nullptr;
    sockaddr_storage endpoint{};
    int endpoint_length = 0;
};

void ReleaseRelayContext(RelayContext* context) {
    if (context != nullptr && context->references.fetch_sub(1) == 1) {
        delete context;
    }
}

void CloseRelayListener(RelayContext* context) {
    const SOCKET listener = context->listener.exchange(INVALID_SOCKET);
    if (listener != INVALID_SOCKET) {
        RealCloseSocket(listener);
    }
}

bool PumpRelayDirection(SOCKET source, SOCKET destination, bool& source_open) {
    std::array<char, 32 * 1024> buffer{};
    const int received = RealRecv(source, buffer.data(), static_cast<int>(buffer.size()), 0);
    if (received > 0) {
        return SendAll(destination, reinterpret_cast<const std::uint8_t*>(buffer.data()),
                       static_cast<std::size_t>(received));
    }
    source_open = false;
    shutdown(destination, SD_SEND);
    return received == 0;
}

void PumpRelay(SOCKET application, SOCKET proxy) {
    bool application_open = true;
    bool proxy_open = true;
    while (application_open || proxy_open) {
        fd_set readable;
        FD_ZERO(&readable);
        if (application_open) {
            FD_SET(application, &readable);
        }
        if (proxy_open) {
            FD_SET(proxy, &readable);
        }
        const int ready = select(0, &readable, nullptr, nullptr, nullptr);
        if (ready == SOCKET_ERROR) {
            break;
        }
        if (application_open && FD_ISSET(application, &readable) != 0 &&
            !PumpRelayDirection(application, proxy, application_open)) {
            break;
        }
        if (proxy_open && FD_ISSET(proxy, &readable) != 0 &&
            !PumpRelayDirection(proxy, application, proxy_open)) {
            break;
        }
    }
}

unsigned __stdcall RelayThread(void* parameter) {
    auto* context = static_cast<RelayContext*>(parameter);
    const SOCKET listener = context->listener.load();
    const SOCKET application = listener == INVALID_SOCKET
                                   ? INVALID_SOCKET
                                   : accept(listener, nullptr, nullptr);
    CloseRelayListener(context);
    if (application == INVALID_SOCKET) {
        ReleaseRelayContext(context);
        return 0;
    }

    const Config& config = GetConfig();
    const auto* proxy_address = reinterpret_cast<const sockaddr*>(&config.proxy);
    const SOCKET proxy = WSASocketW(proxy_address->sa_family, SOCK_STREAM, IPPROTO_TCP,
                                    nullptr, 0, 0);
    if (proxy == INVALID_SOCKET ||
        RealConnect(proxy, proxy_address, config.proxy_length) != 0 ||
        NegotiateSocks5(proxy, reinterpret_cast<const sockaddr*>(&context->destination),
                        context->destination_length) != 0) {
        if (proxy != INVALID_SOCKET) {
            RealCloseSocket(proxy);
        }
        RealCloseSocket(application);
        ReleaseRelayContext(context);
        return 0;
    }

    PumpRelay(application, proxy);
    RealCloseSocket(proxy);
    RealCloseSocket(application);
    ReleaseRelayContext(context);
    return 0;
}

bool CreateLoopbackListener(int family,
                            SOCKET& listener,
                            sockaddr_storage& endpoint,
                            int& endpoint_length) {
    listener = WSASocketW(family, SOCK_STREAM, IPPROTO_TCP, nullptr, 0, 0);
    if (listener == INVALID_SOCKET) {
        return false;
    }
    int result = SOCKET_ERROR;
    if (family == AF_INET) {
        sockaddr_in address{};
        address.sin_family = AF_INET;
        address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        result = bind(listener, reinterpret_cast<const sockaddr*>(&address), sizeof(address));
    } else if (family == AF_INET6) {
        sockaddr_in6 address{};
        address.sin6_family = AF_INET6;
        address.sin6_addr = in6addr_loopback;
        result = bind(listener, reinterpret_cast<const sockaddr*>(&address), sizeof(address));
    }
    if (result != 0 || listen(listener, 1) != 0) {
        RealCloseSocket(listener);
        listener = INVALID_SOCKET;
        return false;
    }
    endpoint_length = sizeof(endpoint);
    if (getsockname(listener, reinterpret_cast<sockaddr*>(&endpoint), &endpoint_length) != 0) {
        RealCloseSocket(listener);
        listener = INVALID_SOCKET;
        return false;
    }
    return true;
}

bool StartRelay(const sockaddr* destination, int destination_length, RelayTicket& ticket) {
    if (destination == nullptr ||
        (destination->sa_family != AF_INET && destination->sa_family != AF_INET6) ||
        destination_length <= 0 ||
        destination_length > static_cast<int>(sizeof(sockaddr_storage))) {
        WSASetLastError(WSAEINVAL);
        return false;
    }
    auto* context = new (std::nothrow) RelayContext();
    if (context == nullptr) {
        WSASetLastError(WSA_NOT_ENOUGH_MEMORY);
        return false;
    }
    context->destination_length = destination_length;
    std::memcpy(&context->destination, destination, static_cast<std::size_t>(destination_length));

    SOCKET listener = INVALID_SOCKET;
    if (!CreateLoopbackListener(destination->sa_family, listener,
                                ticket.endpoint, ticket.endpoint_length)) {
        ReleaseRelayContext(context);
        ReleaseRelayContext(context);
        return false;
    }
    context->listener.store(listener);
    constexpr SIZE_T stack_reservation = 128 * 1024;
    const auto thread_value = _beginthreadex(nullptr, stack_reservation, RelayThread, context,
                                             STACK_SIZE_PARAM_IS_A_RESERVATION, nullptr);
    const HANDLE thread = reinterpret_cast<HANDLE>(thread_value);
    if (thread == nullptr) {
        CloseRelayListener(context);
        ReleaseRelayContext(context);
        ReleaseRelayContext(context);
        WSASetLastError(WSA_NOT_ENOUGH_MEMORY);
        return false;
    }
    CloseHandle(thread);
    ticket.context = context;
    return true;
}

void CancelRelay(RelayTicket& ticket) {
    if (ticket.context != nullptr) {
        CloseRelayListener(ticket.context);
    }
}

void ReleaseRelayTicket(RelayTicket& ticket) {
    ReleaseRelayContext(ticket.context);
    ticket.context = nullptr;
}

int FailSocket(int error) {
    WSASetLastError(error);
    return SOCKET_ERROR;
}

int ConnectThroughRelay(SOCKET socket, const sockaddr* destination, int destination_length) {
    ProxiedPeer existing;
    if (FindProxiedPeer(socket, existing)) {
        return RealConnect(socket, reinterpret_cast<const sockaddr*>(&existing.relay),
                           existing.relay_length);
    }

    RelayTicket ticket;
    if (!StartRelay(destination, destination_length, ticket)) {
        return SOCKET_ERROR;
    }
    RememberProxiedPeer(socket, destination, destination_length,
                        reinterpret_cast<const sockaddr*>(&ticket.endpoint), ticket.endpoint_length,
                        ticket.context);
    const int result = RealConnect(socket, reinterpret_cast<const sockaddr*>(&ticket.endpoint),
                                   ticket.endpoint_length);
    const int error = result == SOCKET_ERROR ? WSAGetLastError() : 0;
    if (result == SOCKET_ERROR && error != WSAEWOULDBLOCK && error != WSAEINPROGRESS &&
        error != WSAEALREADY) {
        CancelRelay(ticket);
        ForgetProxiedPeer(socket);
    }
    ReleaseRelayTicket(ticket);
    if (result == SOCKET_ERROR) {
        WSASetLastError(error);
    }
    return result;
}

int ProxyConnect(SOCKET socket, const sockaddr* destination, int destination_length) {
    const Config& config = GetConfig();
    if (!config.enabled) {
        return RealConnect(socket, destination, destination_length);
    }
    if (!config.valid || destination == nullptr) {
        return FailSocket(WSAEINVAL);
    }

    const int socket_type = SocketType(socket);
    if (socket_type == SOCK_DGRAM) {
        return config.allow_udp_direct ? RealConnect(socket, destination, destination_length)
                                       : FailSocket(WSAEOPNOTSUPP);
    }
    if (socket_type != SOCK_STREAM) {
        return FailSocket(WSAEPROTOTYPE);
    }

    const auto* proxy = reinterpret_cast<const sockaddr*>(&config.proxy);
    if (IsLoopback(destination, destination_length) ||
        SameEndpoint(destination, destination_length, proxy, config.proxy_length)) {
        return RealConnect(socket, destination, destination_length);
    }
    if (IsSocketNonblocking(socket)) {
        return ConnectThroughRelay(socket, destination, destination_length);
    }

    sockaddr_storage proxy_for_socket{};
    int proxy_length = 0;
    if (!MakeProxyEndpoint(destination->sa_family, proxy_for_socket, proxy_length)) {
        return FailSocket(WSAEAFNOSUPPORT);
    }
    if (RealConnect(socket, reinterpret_cast<const sockaddr*>(&proxy_for_socket), proxy_length) != 0) {
        const int error = WSAGetLastError();
        if (error == WSAEWOULDBLOCK || error == WSAEINPROGRESS || error == WSAEALREADY) {
            // The socket was already nonblocking before the hook observed it (for example,
            // when attaching to an existing process). Never destroy a caller-owned handle.
            // Report the unsupported state and let the caller decide whether to close/recreate it.
            SetSocketNonblocking(socket, true);
            return FailSocket(WSAEOPNOTSUPP);
        }
        WSASetLastError(error);
        return SOCKET_ERROR;
    }

    const int error = NegotiateSocks5(socket, destination, destination_length);
    if (error != 0) {
        shutdown(socket, SD_BOTH);
        return FailSocket(error);
    }
    return 0;
}

int WSAAPI HookedConnect(SOCKET socket, const sockaddr* destination, int destination_length) {
    return ProxyConnect(socket, destination, destination_length);
}

int WSAAPI HookedWSAConnect(SOCKET socket,
                            const sockaddr* destination,
                            int destination_length,
                            LPWSABUF caller_data,
                            LPWSABUF callee_data,
                            LPQOS sqos,
                            LPQOS gqos) {
    const Config& config = GetConfig();
    if (!config.enabled) {
        return RealWSAConnect(socket, destination, destination_length, caller_data, callee_data, sqos, gqos);
    }
    if (caller_data != nullptr || callee_data != nullptr || sqos != nullptr || gqos != nullptr) {
        return FailSocket(WSAEOPNOTSUPP);
    }
    return ProxyConnect(socket, destination, destination_length);
}

bool ShouldBlockDatagram(SOCKET socket, const sockaddr* destination, int destination_length) {
    const Config& config = GetConfig();
    return config.enabled && !config.allow_udp_direct && SocketType(socket) == SOCK_DGRAM &&
           !IsLoopback(destination, destination_length);
}

bool ShouldBlockConnectedDatagram(SOCKET socket) {
    const Config& config = GetConfig();
    if (!config.enabled || config.allow_udp_direct || SocketType(socket) != SOCK_DGRAM) {
        return false;
    }
    sockaddr_storage destination{};
    int destination_length = sizeof(destination);
    if (RealGetPeerName(socket, reinterpret_cast<sockaddr*>(&destination), &destination_length) != 0) {
        // A connected UDP send cannot succeed without a peer. Fail closed so an existing
        // pre-injection socket cannot leak traffic while its state is uncertain.
        return true;
    }
    return !IsLoopback(reinterpret_cast<const sockaddr*>(&destination), destination_length);
}

int WSAAPI HookedSend(SOCKET socket, const char* buffer, int length, int flags) {
    if (ShouldBlockConnectedDatagram(socket)) {
        return FailSocket(WSAEOPNOTSUPP);
    }
    return RealSend(socket, buffer, length, flags);
}

int WSAAPI HookedWSASend(SOCKET socket,
                         LPWSABUF buffers,
                         DWORD buffer_count,
                         LPDWORD bytes_sent,
                         DWORD flags,
                         LPWSAOVERLAPPED overlapped,
                         LPWSAOVERLAPPED_COMPLETION_ROUTINE completion) {
    if (ShouldBlockConnectedDatagram(socket)) {
        return FailSocket(WSAEOPNOTSUPP);
    }
    return RealWSASend(socket, buffers, buffer_count, bytes_sent, flags, overlapped, completion);
}

int WSAAPI HookedSendTo(SOCKET socket,
                        const char* buffer,
                        int length,
                        int flags,
                        const sockaddr* destination,
                        int destination_length) {
    if (ShouldBlockDatagram(socket, destination, destination_length)) {
        return FailSocket(WSAEOPNOTSUPP);
    }
    return RealSendTo(socket, buffer, length, flags, destination, destination_length);
}

int WSAAPI HookedWSASendTo(SOCKET socket,
                           LPWSABUF buffers,
                           DWORD buffer_count,
                           LPDWORD bytes_sent,
                           DWORD flags,
                           const sockaddr* destination,
                           int destination_length,
                           LPWSAOVERLAPPED overlapped,
                           LPWSAOVERLAPPED_COMPLETION_ROUTINE completion) {
    if (ShouldBlockDatagram(socket, destination, destination_length)) {
        return FailSocket(WSAEOPNOTSUPP);
    }
    return RealWSASendTo(socket, buffers, buffer_count, bytes_sent, flags, destination,
                         destination_length, overlapped, completion);
}

BOOL PASCAL HookedConnectEx(SOCKET socket,
                            const sockaddr* destination,
                            int destination_length,
                            PVOID send_buffer,
                            DWORD send_length,
                            LPDWORD bytes_sent,
                            LPOVERLAPPED overlapped) {
    LPFN_CONNECTEX original = RealConnectEx;
    if (original == nullptr) {
        original = reinterpret_cast<LPFN_CONNECTEX>(
            InterlockedCompareExchangePointer(&g_fallback_connect_ex, nullptr, nullptr));
    }
    const Config& config = GetConfig();
    const auto* proxy = reinterpret_cast<const sockaddr*>(&config.proxy);
    if ((!config.enabled || IsLoopback(destination, destination_length) ||
         SameEndpoint(destination, destination_length, proxy, config.proxy_length)) &&
        original != nullptr) {
        return original(socket, destination, destination_length, send_buffer, send_length,
                        bytes_sent, overlapped);
    }
    if (original == nullptr || !config.valid || destination == nullptr ||
        SocketType(socket) != SOCK_STREAM) {
        WSASetLastError(WSAEINVAL);
        return FALSE;
    }

    RelayTicket ticket;
    if (!StartRelay(destination, destination_length, ticket)) {
        return FALSE;
    }
    RememberProxiedPeer(socket, destination, destination_length,
                        reinterpret_cast<const sockaddr*>(&ticket.endpoint), ticket.endpoint_length,
                        ticket.context);
    const BOOL result = original(socket, reinterpret_cast<const sockaddr*>(&ticket.endpoint),
                                 ticket.endpoint_length, send_buffer, send_length,
                                 bytes_sent, overlapped);
    const int error = result ? 0 : WSAGetLastError();
    if (!result && error != ERROR_IO_PENDING && error != WSA_IO_PENDING) {
        CancelRelay(ticket);
        ForgetProxiedPeer(socket);
    }
    ReleaseRelayTicket(ticket);
    if (!result) {
        WSASetLastError(error);
    }
    return result;
}

INT PASCAL HookedWSASendMsg(SOCKET socket,
                            LPWSAMSG message,
                            DWORD flags,
                            LPDWORD bytes_sent,
                            LPWSAOVERLAPPED overlapped,
                            LPWSAOVERLAPPED_COMPLETION_ROUTINE completion) {
    LPFN_WSASENDMSG original = RealWSASendMsg;
    if (original == nullptr) {
        original = reinterpret_cast<LPFN_WSASENDMSG>(
            InterlockedCompareExchangePointer(&g_fallback_wsa_send_msg, nullptr, nullptr));
    }
    const Config& config = GetConfig();
    if ((!config.enabled || config.allow_udp_direct ||
         (message != nullptr && message->name != nullptr &&
          IsLoopback(message->name, message->namelen))) &&
        original != nullptr) {
        return original(socket, message, flags, bytes_sent, overlapped, completion);
    }
    WSASetLastError(WSAEOPNOTSUPP);
    return SOCKET_ERROR;
}

int WSAAPI HookedWSAIoctl(SOCKET socket,
                          DWORD control_code,
                          LPVOID input,
                          DWORD input_size,
                          LPVOID output,
                          DWORD output_size,
                          LPDWORD bytes_returned,
                          LPWSAOVERLAPPED overlapped,
                          LPWSAOVERLAPPED_COMPLETION_ROUTINE completion) {
    const int result = RealWSAIoctl(socket, control_code, input, input_size, output, output_size,
                                    bytes_returned, overlapped, completion);
    if (result == 0 && control_code == FIONBIO && input != nullptr &&
        input_size >= sizeof(u_long)) {
        SetSocketNonblocking(socket, *static_cast<const u_long*>(input) != 0);
    }
    if (result != 0 || control_code != SIO_GET_EXTENSION_FUNCTION_POINTER ||
        input == nullptr || input_size < sizeof(GUID) || output == nullptr) {
        return result;
    }

    const auto* requested = static_cast<const GUID*>(input);
    if (IsEqualGUID(*requested, WSAID_CONNECTEX) && output_size >= sizeof(LPFN_CONNECTEX)) {
        if (RealConnectEx == nullptr) {
            InterlockedExchangePointer(&g_fallback_connect_ex,
                                       reinterpret_cast<PVOID>(*static_cast<LPFN_CONNECTEX*>(output)));
        }
        *static_cast<LPFN_CONNECTEX*>(output) = HookedConnectEx;
    } else if (IsEqualGUID(*requested, WSAID_WSASENDMSG) && output_size >= sizeof(LPFN_WSASENDMSG) &&
               GetConfig().enabled && !GetConfig().allow_udp_direct) {
        if (RealWSASendMsg == nullptr) {
            InterlockedExchangePointer(&g_fallback_wsa_send_msg,
                                       reinterpret_cast<PVOID>(*static_cast<LPFN_WSASENDMSG*>(output)));
        }
        *static_cast<LPFN_WSASENDMSG*>(output) = HookedWSASendMsg;
    }
    return result;
}

int WSAAPI HookedIoctlSocket(SOCKET socket, long command, u_long* argument) {
    const int result = RealIoctlSocket(socket, command, argument);
    if (result == 0 && command == FIONBIO && argument != nullptr) {
        SetSocketNonblocking(socket, *argument != 0);
    }
    return result;
}

int WSAAPI HookedWSAEventSelect(SOCKET socket, WSAEVENT event, long events) {
    const int result = RealWSAEventSelect(socket, event, events);
    if (result == 0) {
        SetSocketNonblocking(socket, true);
    }
    return result;
}

int WSAAPI HookedWSAAsyncSelect(SOCKET socket, HWND window, unsigned int message, long events) {
    const int result = RealWSAAsyncSelect(socket, window, message, events);
    if (result == 0) {
        SetSocketNonblocking(socket, true);
    }
    return result;
}

int WSAAPI HookedCloseSocket(SOCKET socket) {
    SetSocketNonblocking(socket, false);
    const int result = RealCloseSocket(socket);
    const int error = result == SOCKET_ERROR ? WSAGetLastError() : 0;
    if (result == 0) {
        ForgetProxiedPeer(socket);
    }
    if (result == SOCKET_ERROR) {
        WSASetLastError(error);
    }
    return result;
}

int WSAAPI HookedGetPeerName(SOCKET socket, sockaddr* address, int* address_length) {
    const int result = RealGetPeerName(socket, address, address_length);
    if (result != 0) {
        return result;
    }
    ProxiedPeer peer;
    if (!FindProxiedPeer(socket, peer)) {
        return 0;
    }
    if (address == nullptr || address_length == nullptr || *address_length < peer.original_length) {
        if (address_length != nullptr) {
            *address_length = peer.original_length;
        }
        return FailSocket(WSAEFAULT);
    }
    std::memcpy(address, &peer.original, static_cast<std::size_t>(peer.original_length));
    *address_length = peer.original_length;
    return 0;
}

BOOL WINAPI HookedCreateProcessW(LPCWSTR application_name,
                                 LPWSTR command_line,
                                 LPSECURITY_ATTRIBUTES process_attributes,
                                 LPSECURITY_ATTRIBUTES thread_attributes,
                                 BOOL inherit_handles,
                                 DWORD creation_flags,
                                 LPVOID environment,
                                 LPCWSTR current_directory,
                                 LPSTARTUPINFOW startup,
                                 LPPROCESS_INFORMATION process) {
    const Config& config = GetConfig();
    if (!config.enabled || !config.inject_children ||
        !easy_net::child::ShouldInject(command_line)) {
        return RealCreateProcessW(application_name, command_line, process_attributes, thread_attributes,
                                  inherit_handles, creation_flags, environment, current_directory,
                                  startup, process);
    }
    return DetourCreateProcessWithDllExW(application_name, command_line, process_attributes,
                                         thread_attributes, inherit_handles, creation_flags, environment,
                                         current_directory, startup, process, g_dll_path, RealCreateProcessW);
}

BOOL WINAPI HookedCreateProcessA(LPCSTR application_name,
                                 LPSTR command_line,
                                 LPSECURITY_ATTRIBUTES process_attributes,
                                 LPSECURITY_ATTRIBUTES thread_attributes,
                                 BOOL inherit_handles,
                                 DWORD creation_flags,
                                 LPVOID environment,
                                 LPCSTR current_directory,
                                 LPSTARTUPINFOA startup,
                                 LPPROCESS_INFORMATION process) {
    const Config& config = GetConfig();
    if (!config.enabled || !config.inject_children ||
        !easy_net::child::ShouldInject(command_line)) {
        return RealCreateProcessA(application_name, command_line, process_attributes, thread_attributes,
                                  inherit_handles, creation_flags, environment, current_directory,
                                  startup, process);
    }
    return DetourCreateProcessWithDllExA(application_name, command_line, process_attributes,
                                         thread_attributes, inherit_handles, creation_flags, environment,
                                         current_directory, startup, process, g_dll_path, RealCreateProcessA);
}

void UpdateProcessThreadsForDetour(std::vector<HANDLE>& opened_threads) {
    DetourUpdateThread(GetCurrentThread());
    const DWORD current_process_id = GetCurrentProcessId();
    const DWORD current_thread_id = GetCurrentThreadId();
    const HANDLE snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0);
    if (snapshot == INVALID_HANDLE_VALUE) {
        return;
    }
    THREADENTRY32 entry{};
    entry.dwSize = sizeof(entry);
    if (Thread32First(snapshot, &entry)) {
        do {
            if (entry.th32OwnerProcessID != current_process_id ||
                entry.th32ThreadID == current_thread_id) {
                continue;
            }
            const HANDLE thread = OpenThread(THREAD_SUSPEND_RESUME | THREAD_GET_CONTEXT |
                                                 THREAD_SET_CONTEXT | THREAD_QUERY_INFORMATION,
                                             FALSE, entry.th32ThreadID);
            if (thread == nullptr) {
                continue;
            }
            if (DetourUpdateThread(thread) == NO_ERROR) {
                opened_threads.push_back(thread);
            } else {
                CloseHandle(thread);
            }
        } while (Thread32Next(snapshot, &entry));
    }
    CloseHandle(snapshot);
}

void CloseDetourThreadHandles(std::vector<HANDLE>& threads) {
    for (const HANDLE thread : threads) {
        CloseHandle(thread);
    }
    threads.clear();
}

template <typename Function>
Function ResolveExtensionFunction(const GUID& identifier, int socket_type, int protocol) {
    WSADATA winsock{};
    if (WSAStartup(MAKEWORD(2, 2), &winsock) != 0) {
        return nullptr;
    }
    Function result = nullptr;
    constexpr std::array<int, 2> families{AF_INET, AF_INET6};
    for (const int family : families) {
        const SOCKET socket = WSASocketW(family, socket_type, protocol, nullptr, 0,
                                         WSA_FLAG_OVERLAPPED);
        if (socket == INVALID_SOCKET) {
            continue;
        }
        DWORD returned = 0;
        if (RealWSAIoctl(socket, SIO_GET_EXTENSION_FUNCTION_POINTER,
                         const_cast<GUID*>(&identifier), sizeof(identifier),
                         &result, sizeof(result), &returned, nullptr, nullptr) == 0 &&
            result != nullptr) {
            RealCloseSocket(socket);
            break;
        }
        RealCloseSocket(socket);
        result = nullptr;
    }
    WSACleanup();
    return result;
}

DWORD WINAPI InstallExtensionHooks(void*) {
    RealConnectEx = ResolveExtensionFunction<LPFN_CONNECTEX>(WSAID_CONNECTEX,
                                                             SOCK_STREAM, IPPROTO_TCP);
    RealWSASendMsg = ResolveExtensionFunction<LPFN_WSASENDMSG>(WSAID_WSASENDMSG,
                                                               SOCK_DGRAM, IPPROTO_UDP);
    if (RealConnectEx == nullptr && RealWSASendMsg == nullptr) {
        return 0;
    }

    DetourTransactionBegin();
    std::vector<HANDLE> threads;
    UpdateProcessThreadsForDetour(threads);
    if (RealConnectEx != nullptr) {
        DetourAttach(reinterpret_cast<PVOID*>(&RealConnectEx), reinterpret_cast<PVOID>(HookedConnectEx));
    }
    if (RealWSASendMsg != nullptr) {
        DetourAttach(reinterpret_cast<PVOID*>(&RealWSASendMsg), reinterpret_cast<PVOID>(HookedWSASendMsg));
    }
    const LONG error = DetourTransactionCommit();
    CloseDetourThreadHandles(threads);
    if (error == NO_ERROR) {
        g_connect_ex_hook_attached.store(RealConnectEx != nullptr);
        g_wsa_send_msg_hook_attached.store(RealWSASendMsg != nullptr);
    } else {
        OutputDebugStringW(L"[Easy-Net Hook] Failed to attach extension function hooks.\n");
    }
    return 0;
}

void AttachHooks() {
    DetourTransactionBegin();
    DetourUpdateThread(GetCurrentThread());
    DetourAttach(reinterpret_cast<PVOID*>(&RealConnect), reinterpret_cast<PVOID>(HookedConnect));
    DetourAttach(reinterpret_cast<PVOID*>(&RealWSAConnect), reinterpret_cast<PVOID>(HookedWSAConnect));
    DetourAttach(reinterpret_cast<PVOID*>(&RealSendTo), reinterpret_cast<PVOID>(HookedSendTo));
    DetourAttach(reinterpret_cast<PVOID*>(&RealWSASendTo), reinterpret_cast<PVOID>(HookedWSASendTo));
    DetourAttach(reinterpret_cast<PVOID*>(&RealSend), reinterpret_cast<PVOID>(HookedSend));
    DetourAttach(reinterpret_cast<PVOID*>(&RealWSASend), reinterpret_cast<PVOID>(HookedWSASend));
    DetourAttach(reinterpret_cast<PVOID*>(&RealWSAIoctl), reinterpret_cast<PVOID>(HookedWSAIoctl));
    DetourAttach(reinterpret_cast<PVOID*>(&RealIoctlSocket), reinterpret_cast<PVOID>(HookedIoctlSocket));
    DetourAttach(reinterpret_cast<PVOID*>(&RealWSAEventSelect), reinterpret_cast<PVOID>(HookedWSAEventSelect));
    DetourAttach(reinterpret_cast<PVOID*>(&RealWSAAsyncSelect), reinterpret_cast<PVOID>(HookedWSAAsyncSelect));
    DetourAttach(reinterpret_cast<PVOID*>(&RealCloseSocket), reinterpret_cast<PVOID>(HookedCloseSocket));
    DetourAttach(reinterpret_cast<PVOID*>(&RealGetPeerName), reinterpret_cast<PVOID>(HookedGetPeerName));
    DetourAttach(reinterpret_cast<PVOID*>(&RealGetAddrInfoA), reinterpret_cast<PVOID>(HookedGetAddrInfoA));
    DetourAttach(reinterpret_cast<PVOID*>(&RealFreeAddrInfoA), reinterpret_cast<PVOID>(HookedFreeAddrInfoA));
    DetourAttach(reinterpret_cast<PVOID*>(&RealGetAddrInfoW), reinterpret_cast<PVOID>(HookedGetAddrInfoW));
    DetourAttach(reinterpret_cast<PVOID*>(&RealFreeAddrInfoW), reinterpret_cast<PVOID>(HookedFreeAddrInfoW));
    DetourAttach(reinterpret_cast<PVOID*>(&RealGetAddrInfoExA), reinterpret_cast<PVOID>(HookedGetAddrInfoExA));
    DetourAttach(reinterpret_cast<PVOID*>(&RealGetAddrInfoExW), reinterpret_cast<PVOID>(HookedGetAddrInfoExW));
    DetourAttach(reinterpret_cast<PVOID*>(&RealCreateProcessW), reinterpret_cast<PVOID>(HookedCreateProcessW));
    DetourAttach(reinterpret_cast<PVOID*>(&RealCreateProcessA), reinterpret_cast<PVOID>(HookedCreateProcessA));
    const LONG error = DetourTransactionCommit();
    if (error != NO_ERROR) {
        OutputDebugStringW(L"[Easy-Net Hook] Failed to attach one or more hooks.\n");
    }
}

void DetachHooks() {
    DetourTransactionBegin();
    DetourUpdateThread(GetCurrentThread());
    if (g_connect_ex_hook_attached.load() && RealConnectEx != nullptr) {
        DetourDetach(reinterpret_cast<PVOID*>(&RealConnectEx), reinterpret_cast<PVOID>(HookedConnectEx));
    }
    if (g_wsa_send_msg_hook_attached.load() && RealWSASendMsg != nullptr) {
        DetourDetach(reinterpret_cast<PVOID*>(&RealWSASendMsg), reinterpret_cast<PVOID>(HookedWSASendMsg));
    }
    DetourDetach(reinterpret_cast<PVOID*>(&RealConnect), reinterpret_cast<PVOID>(HookedConnect));
    DetourDetach(reinterpret_cast<PVOID*>(&RealWSAConnect), reinterpret_cast<PVOID>(HookedWSAConnect));
    DetourDetach(reinterpret_cast<PVOID*>(&RealSendTo), reinterpret_cast<PVOID>(HookedSendTo));
    DetourDetach(reinterpret_cast<PVOID*>(&RealWSASendTo), reinterpret_cast<PVOID>(HookedWSASendTo));
    DetourDetach(reinterpret_cast<PVOID*>(&RealSend), reinterpret_cast<PVOID>(HookedSend));
    DetourDetach(reinterpret_cast<PVOID*>(&RealWSASend), reinterpret_cast<PVOID>(HookedWSASend));
    DetourDetach(reinterpret_cast<PVOID*>(&RealWSAIoctl), reinterpret_cast<PVOID>(HookedWSAIoctl));
    DetourDetach(reinterpret_cast<PVOID*>(&RealIoctlSocket), reinterpret_cast<PVOID>(HookedIoctlSocket));
    DetourDetach(reinterpret_cast<PVOID*>(&RealWSAEventSelect), reinterpret_cast<PVOID>(HookedWSAEventSelect));
    DetourDetach(reinterpret_cast<PVOID*>(&RealWSAAsyncSelect), reinterpret_cast<PVOID>(HookedWSAAsyncSelect));
    DetourDetach(reinterpret_cast<PVOID*>(&RealCloseSocket), reinterpret_cast<PVOID>(HookedCloseSocket));
    DetourDetach(reinterpret_cast<PVOID*>(&RealGetPeerName), reinterpret_cast<PVOID>(HookedGetPeerName));
    DetourDetach(reinterpret_cast<PVOID*>(&RealGetAddrInfoA), reinterpret_cast<PVOID>(HookedGetAddrInfoA));
    DetourDetach(reinterpret_cast<PVOID*>(&RealFreeAddrInfoA), reinterpret_cast<PVOID>(HookedFreeAddrInfoA));
    DetourDetach(reinterpret_cast<PVOID*>(&RealGetAddrInfoW), reinterpret_cast<PVOID>(HookedGetAddrInfoW));
    DetourDetach(reinterpret_cast<PVOID*>(&RealFreeAddrInfoW), reinterpret_cast<PVOID>(HookedFreeAddrInfoW));
    DetourDetach(reinterpret_cast<PVOID*>(&RealGetAddrInfoExA), reinterpret_cast<PVOID>(HookedGetAddrInfoExA));
    DetourDetach(reinterpret_cast<PVOID*>(&RealGetAddrInfoExW), reinterpret_cast<PVOID>(HookedGetAddrInfoExW));
    DetourDetach(reinterpret_cast<PVOID*>(&RealCreateProcessW), reinterpret_cast<PVOID>(HookedCreateProcessW));
    DetourDetach(reinterpret_cast<PVOID*>(&RealCreateProcessA), reinterpret_cast<PVOID>(HookedCreateProcessA));
    DetourTransactionCommit();
}

}  // namespace

BOOL APIENTRY DllMain(HMODULE module, DWORD reason, LPVOID reserved) {
    if (DetourIsHelperProcess()) {
        return TRUE;
    }
    if (reason == DLL_PROCESS_ATTACH) {
        DetourRestoreAfterWith();
        DisableThreadLibraryCalls(module);
        GetModuleFileNameA(module, g_dll_path, static_cast<DWORD>(std::size(g_dll_path)));
        GetConfig();
        AttachHooks();
        // CreateThread will not execute the entry point until DLL initialization completes.
        // Avoid invoking CRT thread startup while the loader lock is held.
        const HANDLE extension_thread =
            CreateThread(nullptr, 0, InstallExtensionHooks, nullptr, 0, nullptr);
        if (extension_thread != nullptr) {
            CloseHandle(extension_thread);
        }
    } else if (reason == DLL_PROCESS_DETACH && reserved == nullptr) {
        DetachHooks();
    }
    return TRUE;
}
