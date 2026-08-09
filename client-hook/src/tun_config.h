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

struct Endpoint {
    std::string host;
    std::uint16_t port = 0;
};

struct Config {
    Endpoint proxy;
    std::string username;
    std::string password;
    std::string dns_host;
    std::uint16_t dns_port = 53;
    std::string log_path;
    std::string log_level = "warn";
    UdpMode udp_mode = UdpMode::block;
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
        "WeChat.exe",       "Weixin.exe",       "WeChatAppEx.exe",
        "WeChatBrowser.exe", "WeChatOCR.exe",    "WeChatPlayer.exe",
        "WeChatUtility.exe", "WeChatWeb.exe",    "WeChatUpdate.exe",
        "xwechat.exe",
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
         << "\"interface_name\": \"easy-net-wechat\", \"address\": [\"172.19.0.1/30\"], "
         << "\"mtu\": 1500, \"auto_route\": true, \"strict_route\": false, "
         << "\"stack\": \"mixed\"}],\n"
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
