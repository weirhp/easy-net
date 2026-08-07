#pragma once

#include <windows.h>

#include <algorithm>
#include <cstdint>
#include <iterator>
#include <string>

namespace easy_net::ipc {

constexpr std::uint32_t kConfigMagic = 0x454E484B;  // ENHK
constexpr std::uint32_t kConfigVersion = 1;

struct ConfigBlock {
    std::uint32_t magic = kConfigMagic;
    std::uint32_t version = kConfigVersion;
    std::uint32_t inject_children = 1;
    std::uint32_t allow_udp_direct = 0;
    wchar_t proxy[128]{};
    wchar_t username[256]{};
    wchar_t password[256]{};
    wchar_t dns[128]{};
};

inline std::wstring ConfigMappingName(DWORD process_id) {
    return L"Local\\EasyNetHookConfig-" + std::to_wstring(process_id);
}

template <std::size_t Size>
bool CopyString(wchar_t (&destination)[Size], const std::wstring& source) {
    if (source.size() >= Size) {
        return false;
    }
    std::copy(source.begin(), source.end(), destination);
    destination[source.size()] = L'\0';
    return true;
}

inline bool IsValid(const ConfigBlock& block) {
    return block.magic == kConfigMagic && block.version == kConfigVersion &&
           block.proxy[std::size(block.proxy) - 1] == L'\0' &&
           block.username[std::size(block.username) - 1] == L'\0' &&
           block.password[std::size(block.password) - 1] == L'\0' &&
           block.dns[std::size(block.dns) - 1] == L'\0';
}

}  // namespace easy_net::ipc
