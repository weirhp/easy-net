#pragma once

#include <shellapi.h>

#include <filesystem>
#include <fstream>
#include <iterator>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

#include "lite_engine.h"

namespace easy_net::lite_control {

constexpr wchar_t kDefaultControl[] = L"http://127.0.0.1:18081";
constexpr char kApplication[] = "easy-net-lite";

inline std::optional<std::wstring> LiteStatusControl() {
    const DWORD size = GetEnvironmentVariableW(L"APPDATA", nullptr, 0);
    if (size == 0) {
        return std::nullopt;
    }
    std::vector<wchar_t> app_data(size);
    if (GetEnvironmentVariableW(L"APPDATA", app_data.data(), size) == 0) {
        return std::nullopt;
    }
    const std::filesystem::path status_path =
        std::filesystem::path(app_data.data()) / L"Easy-Net Lite/status.json";
    std::ifstream input(status_path, std::ios::binary);
    if (!input) {
        return std::nullopt;
    }
    std::string body((std::istreambuf_iterator<char>(input)),
                     std::istreambuf_iterator<char>());
    if (body.size() > 64 * 1024) {
        return std::nullopt;
    }
    const auto application = lite_engine::JsonString(body, "application");
    const auto control = lite_engine::JsonString(body, "control");
    if (!application || *application != kApplication || !control) {
        return std::nullopt;
    }
    const std::wstring wide = lite_engine::Utf8ToWide(*control);
    if (!wide.starts_with(L"http://127.0.0.1:")) {
        return std::nullopt;
    }
    return wide;
}

inline bool ProbeLiteAt(const std::wstring& control, std::wstring& token) {
    const auto ping = lite_engine::HttpRequest(
        L"GET", control + L"/api/ping", {}, {});
    if (ping.status != 200) {
        return false;
    }
    const auto application = lite_engine::JsonString(ping.body, "application");
    if (!application || *application != kApplication) {
        return false;
    }
    const auto state = lite_engine::HttpRequest(
        L"GET", control + L"/api/state", {}, {});
    if (state.status != 200) {
        return false;
    }
    const auto raw_token = lite_engine::JsonString(state.body, "token");
    if (!raw_token || raw_token->empty()) {
        return false;
    }
    token = lite_engine::Utf8ToWide(*raw_token);
    return !token.empty();
}

inline bool ProbeLite(std::wstring& token, std::wstring* discovered_control = nullptr) {
    std::wstring control = kDefaultControl;
    if (!ProbeLiteAt(control, token)) {
        const auto status_control = LiteStatusControl();
        if (!status_control || !ProbeLiteAt(*status_control, token)) {
            return false;
        }
        control = *status_control;
    }
    if (discovered_control != nullptr) {
        *discovered_control = std::move(control);
    }
    return true;
}

inline std::filesystem::path FindLiteExecutable(const std::filesystem::path& hook_path) {
    const auto directory = hook_path.has_parent_path() ? hook_path.parent_path()
                                                       : std::filesystem::current_path();
    const std::filesystem::path candidates[] = {
        directory / L"Easy-Net-Lite.exe",
        directory / L"Easy-Net Lite.exe",
        directory / L"easy-net.exe",
    };
    std::error_code error;
    for (const auto& candidate : candidates) {
        if (std::filesystem::is_regular_file(candidate, error)) {
            return candidate;
        }
    }
    return {};
}

inline bool StartLiteProcess(const std::filesystem::path& lite_path, const std::wstring& arguments,
                             std::wstring& error) {
    std::wstring command = L"\"" + lite_path.wstring() + L"\"";
    if (!arguments.empty()) {
        command += L" " + arguments;
    }
    std::vector<wchar_t> mutable_command(command.begin(), command.end());
    mutable_command.push_back(L'\0');
    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    PROCESS_INFORMATION process{};
    if (!CreateProcessW(lite_path.c_str(), mutable_command.data(), nullptr, nullptr, FALSE,
                        CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS, nullptr,
                        lite_path.parent_path().c_str(), &startup, &process)) {
        error = L"无法启动 Easy-Net Lite。";
        return false;
    }
    CloseHandle(process.hThread);
    CloseHandle(process.hProcess);
    return true;
}

inline bool WaitForLite(int timeout_ms) {
    const ULONGLONG deadline = GetTickCount64() + static_cast<ULONGLONG>(timeout_ms);
    std::wstring token;
    while (GetTickCount64() < deadline) {
        if (ProbeLite(token)) {
            return true;
        }
        Sleep(200);
    }
    return false;
}

inline bool StartLaunch(const std::wstring& id, std::wstring& error) {
    std::wstring token;
    std::wstring control;
    if (!ProbeLite(token, &control)) {
        error = L"Easy-Net Lite 未运行";
        return false;
    }
    const auto result = lite_engine::HttpRequest(
        L"POST", control + L"/api/launches/" + id + L"/start", "{}", token);
    if (result.status == 200) {
        return true;
    }
    if (result.status == 404) {
        error = L"not_found";
        return false;
    }
    const auto message = lite_engine::JsonString(result.body, "error");
    if (message && !message->empty()) {
        error = lite_engine::Utf8ToWide(*message);
        return false;
    }
    error = L"Easy-Net Lite 无法启动该应用。";
    return false;
}

inline void OpenAppsPage(const std::wstring& control = kDefaultControl) {
    const std::wstring url = control + L"/#apps";
    ShellExecuteW(nullptr, L"open", url.c_str(), nullptr, nullptr, SW_SHOWNORMAL);
}

inline int OpenLiteApps(const std::wstring& hook_path) {
    std::wstring token;
    std::wstring control;
    if (ProbeLite(token, &control)) {
        OpenAppsPage(control);
        return 0;
    }
    const auto lite_path = FindLiteExecutable(hook_path);
    if (lite_path.empty()) {
        MessageBoxW(nullptr,
                    L"请先启动 Easy-Net Lite。\r\n"
                    L"未在当前目录找到 Easy-Net-Lite.exe。应用启动入口已移到 Lite 管理页的“应用”标签。",
                    L"Easy-Net Hook", MB_OK | MB_ICONINFORMATION);
        return 3;
    }
    std::wstring error;
    if (!StartLiteProcess(lite_path, L"--open-apps", error)) {
        MessageBoxW(nullptr, error.c_str(), L"Easy-Net Hook", MB_OK | MB_ICONERROR);
        return 3;
    }
    if (!WaitForLite(15000)) {
        MessageBoxW(nullptr, L"已尝试启动 Easy-Net Lite，但管理界面没有在 15 秒内就绪。",
                    L"Easy-Net Hook", MB_OK | MB_ICONWARNING);
        return 3;
    }
    return 0;
}

// Returns 0 on success, a positive process exit code on failure, or -1 to fall back
// to the legacy launcher-entries.tsv shortcut path.
inline int DispatchLaunchEntry(const std::wstring& hook_path, const std::wstring& id) {
    std::wstring token;
    std::wstring error;
    if (ProbeLite(token)) {
        if (StartLaunch(id, error)) {
            return 0;
        }
        if (error == L"not_found") {
            return -1;
        }
        MessageBoxW(nullptr, error.c_str(), L"启动失败", MB_OK | MB_ICONERROR);
        return 3;
    }
    const auto lite_path = FindLiteExecutable(hook_path);
    if (lite_path.empty()) {
        return -1;
    }
    // Start the full Lite control plane without opening a browser, then submit the
    // launch through its authenticated local API.  Starting Lite with
    // --launch-entry used to return before the entry had actually been accepted,
    // which made broken shortcuts look successful and hid errors in Lite's log.
    if (!StartLiteProcess(lite_path, L"--background", error)) {
        MessageBoxW(nullptr, error.c_str(), L"启动失败", MB_OK | MB_ICONERROR);
        return 3;
    }
    if (!WaitForLite(15000)) {
        MessageBoxW(nullptr,
                    L"已启动 Easy-Net Lite，但本地管理服务没有在 15 秒内就绪。",
                    L"启动失败", MB_OK | MB_ICONERROR);
        return 3;
    }
    if (!StartLaunch(id, error)) {
        const std::wstring message =
            error == L"not_found" ? L"启动入口不存在，可能已在 Easy-Net Lite 中删除。"
                                  : error;
        MessageBoxW(nullptr, message.c_str(), L"启动失败", MB_OK | MB_ICONERROR);
        return 3;
    }
    return 0;
}

}  // namespace easy_net::lite_control
