#pragma once

#include <windows.h>
#include <appmodel.h>

#include <filesystem>
#include <optional>
#include <vector>

namespace easy_net::chatgpt {

inline std::vector<std::filesystem::path> PackageRoots() {
    constexpr wchar_t package_family[] = L"OpenAI.Codex_2p2nqsd0c76g0";
    UINT32 count = 0;
    UINT32 buffer_length = 0;
    LONG result = GetPackagesByPackageFamily(package_family, &count, nullptr,
                                             &buffer_length, nullptr);
    if (result != ERROR_INSUFFICIENT_BUFFER || count == 0 || buffer_length == 0) {
        return {};
    }

    std::vector<PWSTR> package_names(count);
    std::vector<wchar_t> name_buffer(buffer_length);
    result = GetPackagesByPackageFamily(package_family, &count, package_names.data(),
                                        &buffer_length, name_buffer.data());
    if (result != ERROR_SUCCESS) {
        return {};
    }

    std::vector<std::filesystem::path> roots;
    roots.reserve(count);
    for (UINT32 index = 0; index < count; ++index) {
        UINT32 path_length = 0;
        result = GetStagedPackagePathByFullName(package_names[index], &path_length, nullptr);
        if (result != ERROR_INSUFFICIENT_BUFFER || path_length == 0) {
            continue;
        }
        std::vector<wchar_t> path_buffer(path_length);
        result = GetStagedPackagePathByFullName(package_names[index], &path_length,
                                                path_buffer.data());
        if (result == ERROR_SUCCESS) {
            roots.emplace_back(path_buffer.data());
        }
    }
    return roots;
}

inline std::optional<std::filesystem::path> FindExecutable() {
    for (const auto& root : PackageRoots()) {
        const std::filesystem::path executable = root / L"app/ChatGPT.exe";
        if (std::filesystem::is_regular_file(executable)) {
            return executable;
        }
    }
    return std::nullopt;
}

inline std::optional<std::filesystem::path> FindOfficialIcon() {
    for (const auto& root : PackageRoots()) {
        const std::filesystem::path icon = root / L"app/resources/icon-chatgpt.ico";
        if (std::filesystem::is_regular_file(icon)) {
            return icon;
        }
    }
    return std::nullopt;
}

}  // namespace easy_net::chatgpt
