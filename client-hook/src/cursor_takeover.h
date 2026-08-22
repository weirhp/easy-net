#pragma once

#include <cctype>
#include <optional>
#include <string>
#include <string_view>

#include "child_injection.h"

namespace easy_net::cursor {

inline std::optional<std::string> NodeProxyFromSharedProfile(std::string_view json) {
    constexpr std::string_view key = "\"EasyNetCursorNodeProxy\"";
    std::size_t position = json.find(key);
    if (position == std::string_view::npos) {
        return std::nullopt;
    }
    position += key.size();
    while (position < json.size() &&
           std::isspace(static_cast<unsigned char>(json[position])) != 0) {
        ++position;
    }
    if (position >= json.size() || json[position++] != ':') {
        return std::nullopt;
    }
    while (position < json.size() &&
           std::isspace(static_cast<unsigned char>(json[position])) != 0) {
        ++position;
    }
    if (position >= json.size() || json[position++] != '"') {
        return std::nullopt;
    }
    const std::size_t start = position;
    while (position < json.size() && json[position] != '"') {
        // The value is emitted from a validated literal IP:port endpoint and
        // therefore never needs JSON escapes. Rejecting escapes keeps this
        // tiny parser strict instead of partially decoding untrusted input.
        const unsigned char character = static_cast<unsigned char>(json[position]);
        if (character == '\\' || character < 0x20 || character > 0x7e) {
            return std::nullopt;
        }
        ++position;
    }
    if (position >= json.size() || position == start) {
        return std::nullopt;
    }
    return std::string(json.substr(start, position - start));
}

inline bool ShouldInjectTakeoverProcess(std::wstring_view command_line) {
    if (command_line.empty()) {
        return true;
    }
    const std::wstring terminated(command_line);
    return easy_net::child::ShouldInject(terminated.c_str());
}

}  // namespace easy_net::cursor
