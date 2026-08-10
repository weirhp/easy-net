#pragma once

#include <algorithm>
#include <cctype>
#include <cstdint>
#include <sstream>
#include <string>
#include <string_view>
#include <vector>

namespace easy_net::tun {

enum class UdpMode {
    automatic,
    proxy,
    block,
    direct,
};

enum class Stack {
    system,
    mixed,
    gvisor,
};

struct Endpoint {
    std::string host;
    std::uint16_t port = 0;
};

struct Config {
    Endpoint proxy;
    std::string interface_name = "easy-net-wechat";
    std::string username;
    std::string password;
    std::string dns_host;
    std::uint16_t dns_port = 53;
    std::string log_path;
    std::string log_level = "warn";
    UdpMode udp_mode = UdpMode::block;
    Stack stack = Stack::system;
    std::vector<std::string> route_exclude_addresses{
        "10.0.0.0/8",
        "100.64.0.0/10",
        "169.254.0.0/16",
        "172.16.0.0/12",
        "192.168.0.0/16",
        "fc00::/7",
        "fe80::/10",
    };
};

inline bool ParsePort(std::string_view value, std::uint16_t& port) {
    if (value.empty()) {
        return false;
    }
    unsigned long parsed = 0;
    for (const char character : value) {
        if (character < '0' || character > '9') {
            return false;
        }
        parsed = parsed * 10 + static_cast<unsigned long>(character - '0');
        if (parsed > 65535) {
            return false;
        }
    }
    if (parsed == 0) {
        return false;
    }
    port = static_cast<std::uint16_t>(parsed);
    return true;
}

inline bool ParseEndpoint(std::string_view value, Endpoint& endpoint,
                          std::uint16_t default_port = 0) {
    endpoint = {};
    if (value.empty()) {
        return false;
    }
    if (value.front() == '[') {
        const std::size_t close = value.find(']');
        if (close == std::string_view::npos || close == 1) {
            return false;
        }
        endpoint.host.assign(value.substr(1, close - 1));
        if (close + 1 == value.size()) {
            endpoint.port = default_port;
            return endpoint.port != 0;
        }
        if (value[close + 1] != ':' ||
            !ParsePort(value.substr(close + 2), endpoint.port)) {
            return false;
        }
        return true;
    }

    const std::size_t colon = value.rfind(':');
    if (colon == std::string_view::npos) {
        endpoint.host.assign(value);
        endpoint.port = default_port;
        return !endpoint.host.empty() && endpoint.port != 0;
    }
    // Bare IPv6 literals are accepted only when a default port is available. A port on IPv6
    // must use [address]:port so that the split is unambiguous.
    if (value.find(':') != colon) {
        endpoint.host.assign(value);
        endpoint.port = default_port;
        return endpoint.port != 0;
    }
    endpoint.host.assign(value.substr(0, colon));
    return !endpoint.host.empty() && ParsePort(value.substr(colon + 1), endpoint.port);
}

inline bool ParseUdpMode(std::string_view value, UdpMode& mode) {
    std::string normalized(value);
    std::transform(normalized.begin(), normalized.end(), normalized.begin(),
                   [](unsigned char character) { return static_cast<char>(std::tolower(character)); });
    if (normalized == "auto") {
        mode = UdpMode::automatic;
    } else if (normalized == "proxy") {
        mode = UdpMode::proxy;
    } else if (normalized == "block") {
        mode = UdpMode::block;
    } else if (normalized == "direct") {
        mode = UdpMode::direct;
    } else {
        return false;
    }
    return true;
}

inline bool ParseStack(std::string_view value, Stack& stack) {
    std::string normalized(value);
    std::transform(normalized.begin(), normalized.end(), normalized.begin(),
                   [](unsigned char character) { return static_cast<char>(std::tolower(character)); });
    if (normalized == "system") {
        stack = Stack::system;
    } else if (normalized == "mixed") {
        stack = Stack::mixed;
    } else if (normalized == "gvisor") {
        stack = Stack::gvisor;
    } else {
        return false;
    }
    return true;
}

inline const char* StackName(Stack stack) {
    switch (stack) {
        case Stack::system: return "system";
        case Stack::mixed: return "mixed";
        case Stack::gvisor: return "gvisor";
    }
    return "system";
}

inline bool ParseDecimal(std::string_view value, unsigned int maximum) {
    if (value.empty()) {
        return false;
    }
    unsigned int parsed = 0;
    for (const char character : value) {
        if (character < '0' || character > '9') {
            return false;
        }
        const unsigned int digit = static_cast<unsigned int>(character - '0');
        if (parsed > (maximum - digit) / 10) {
            return false;
        }
        parsed = parsed * 10 + digit;
    }
    return true;
}

inline bool IsIpv4Address(std::string_view value) {
    std::size_t start = 0;
    for (int part = 0; part < 4; ++part) {
        const std::size_t end = value.find('.', start);
        if ((part < 3 && end == std::string_view::npos) ||
            (part == 3 && end != std::string_view::npos)) {
            return false;
        }
        const std::size_t length = (end == std::string_view::npos ? value.size() : end) - start;
        if (length == 0 || length > 3 || !ParseDecimal(value.substr(start, length), 255)) {
            return false;
        }
        start = end == std::string_view::npos ? value.size() : end + 1;
    }
    return start == value.size();
}

inline bool IsRoutePrefix(std::string_view value) {
    const std::size_t slash = value.find('/');
    if (slash == std::string_view::npos || slash == 0 || slash + 1 >= value.size() ||
        value.find('/', slash + 1) != std::string_view::npos) {
        return false;
    }
    const std::string_view address = value.substr(0, slash);
    const std::string_view prefix = value.substr(slash + 1);
    if (address.find(':') == std::string_view::npos) {
        return IsIpv4Address(address) && ParseDecimal(prefix, 32);
    }
    if (!ParseDecimal(prefix, 128)) {
        return false;
    }
    bool has_colon = false;
    for (const unsigned char character : address) {
        if (character == ':') {
            has_colon = true;
        } else if (character != '.' && !std::isxdigit(character)) {
            return false;
        }
    }
    return has_colon;
}

inline bool AppendRouteExclusions(std::string_view value,
                                  std::vector<std::string>& exclusions) {
    std::size_t start = 0;
    while (start <= value.size()) {
        const std::size_t comma = value.find(',', start);
        const std::size_t end = comma == std::string_view::npos ? value.size() : comma;
        std::string_view item = value.substr(start, end - start);
        while (!item.empty() && std::isspace(static_cast<unsigned char>(item.front()))) {
            item.remove_prefix(1);
        }
        while (!item.empty() && std::isspace(static_cast<unsigned char>(item.back()))) {
            item.remove_suffix(1);
        }
        if (!IsRoutePrefix(item)) {
            return false;
        }
        const std::string prefix(item);
        if (std::find(exclusions.begin(), exclusions.end(), prefix) == exclusions.end()) {
            exclusions.push_back(prefix);
        }
        if (comma == std::string_view::npos) {
            break;
        }
        start = comma + 1;
    }
    return true;
}

inline std::string JsonString(std::string_view value) {
    std::string output;
    output.reserve(value.size() + 2);
    output.push_back('"');
    constexpr char hex[] = "0123456789abcdef";
    for (const unsigned char character : value) {
        switch (character) {
            case '"': output += "\\\""; break;
            case '\\': output += "\\\\"; break;
            case '\b': output += "\\b"; break;
            case '\f': output += "\\f"; break;
            case '\n': output += "\\n"; break;
            case '\r': output += "\\r"; break;
            case '\t': output += "\\t"; break;
            default:
                if (character < 0x20) {
                    output += "\\u00";
                    output.push_back(hex[(character >> 4) & 0x0f]);
                    output.push_back(hex[character & 0x0f]);
                } else {
                    output.push_back(static_cast<char>(character));
                }
                break;
        }
    }
    output.push_back('"');
    return output;
}

inline const std::vector<std::string>& WeChatProcessNames() {
    static const std::vector<std::string> names{
        "WeChat.exe",        "Weixin.exe",       "WeChatApp.exe",
        "WeChatAppEx.exe",   "WeChatBrowser.exe", "WeChatOCR.exe",
        "WeChatPlayer.exe",  "WeChatUtility.exe", "WeChatWeb.exe",
        "WeChatUpdate.exe",  "xwechat.exe",
    };
    return names;
}

inline std::string ProcessNameArray() {
    std::string output = "[";
    const auto& names = WeChatProcessNames();
    for (std::size_t index = 0; index < names.size(); ++index) {
        if (index != 0) {
            output.push_back(',');
        }
        output += JsonString(names[index]);
    }
    output.push_back(']');
    return output;
}

inline std::string JsonStringArray(const std::vector<std::string>& values) {
    std::string output = "[";
    for (std::size_t index = 0; index < values.size(); ++index) {
        if (index != 0) {
            output.push_back(',');
        }
        output += JsonString(values[index]);
    }
    output.push_back(']');
    return output;
}

inline std::string BuildConfig(const Config& config) {
    const std::string processes = ProcessNameArray();
    std::ostringstream json;
    json << "{\n"
         << "  \"log\": {\"level\": " << JsonString(config.log_level)
         << ", \"timestamp\": true";
    if (!config.log_path.empty()) {
        json << ", \"output\": " << JsonString(config.log_path);
    }
    json << "},\n";

    if (!config.dns_host.empty()) {
        json << "  \"dns\": {\"servers\": [{\"type\": \"udp\", \"tag\": \"configured-dns\", "
             << "\"server\": " << JsonString(config.dns_host)
             << ", \"server_port\": " << config.dns_port
             << ", \"detour\": \"direct\"}], \"final\": \"configured-dns\"},\n";
    }

    json << "  \"inbounds\": [{\"type\": \"tun\", \"tag\": \"tun-in\", "
         << "\"interface_name\": " << JsonString(config.interface_name)
         << ", \"address\": [\"172.19.0.1/30\"], "
         << "\"mtu\": 1500, \"auto_route\": true, \"strict_route\": false, "
         << "\"stack\": " << JsonString(StackName(config.stack));
    if (!config.route_exclude_addresses.empty()) {
        json << ", \"route_exclude_address\": "
             << JsonStringArray(config.route_exclude_addresses);
    }
    json << "}],\n"
         << "  \"outbounds\": [{\"type\": \"socks\", \"tag\": \"socks-out\", "
         << "\"server\": " << JsonString(config.proxy.host)
         << ", \"server_port\": " << config.proxy.port << ", \"version\": \"5\"";
    if (!config.username.empty() || !config.password.empty()) {
        json << ", \"username\": " << JsonString(config.username)
             << ", \"password\": " << JsonString(config.password);
    }
    json << "}, {\"type\": \"direct\", \"tag\": \"direct\"}],\n"
         << "  \"route\": {\"auto_detect_interface\": true, \"rules\": [\n";
    if (!config.dns_host.empty()) {
        json << "    {\"protocol\": \"dns\", \"action\": \"hijack-dns\"},\n";
    }
    json << "    {\"ip_is_private\": true, \"action\": \"route\", \"outbound\": \"direct\"},\n";
    if (config.udp_mode == UdpMode::block) {
        json << "    {\"network\": \"udp\", \"process_name\": " << processes
             << ", \"action\": \"reject\"},\n";
    } else if (config.udp_mode == UdpMode::direct) {
        json << "    {\"network\": \"udp\", \"process_name\": " << processes
             << ", \"action\": \"route\", \"outbound\": \"direct\"},\n";
    }
    json << "    {\"process_name\": " << processes
         << ", \"action\": \"route\", \"outbound\": \"socks-out\"}\n"
         << "  ], \"final\": \"direct\"}\n"
         << "}\n";
    return json.str();
}

}  // namespace easy_net::tun
