#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <appmodel.h>
#include <detours.h>
#include <shobjidl.h>
#include <shellapi.h>
#include <tlhelp32.h>

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <cwchar>
#include <filesystem>
#include <iomanip>
#include <iostream>
#include <iterator>
#include <optional>
#include <sstream>
#include <string>
#include <string_view>
#include <system_error>
#include <thread>
#include <unordered_map>
#include <utility>
#include <vector>

#include "browser_proxy.h"
#include "config_ipc.h"
#include "dns_resolver.h"
#include "launcher_gui.h"
#include "socks5_health.h"
#include "tun_config.h"
#include "wechat_supervisor.h"
#include "windivert_profile.h"

namespace {

std::optional<std::string> ToUtf8(std::wstring_view value) {
    if (value.empty()) {
        return std::string{};
    }
    const int size = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
                                         static_cast<int>(value.size()), nullptr, 0, nullptr,
                                         nullptr);
    if (size <= 0) {
        return std::nullopt;
    }
    std::string result(static_cast<std::size_t>(size), '\0');
    if (WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
                            static_cast<int>(value.size()), result.data(), size, nullptr,
                            nullptr) != size) {
        return std::nullopt;
    }
    return result;
}

struct Options {
    std::wstring proxy;
    std::wstring username;
    std::wstring password;
    std::wstring dns;
    bool inject_children = true;
    bool allow_udp_direct = false;
    bool detach = false;
    bool gui_worker = false;
    bool antigravity = false;
    bool antigravity_isolated = false;
    bool cursor = false;
    bool cursor_isolated = false;
    bool chatgpt_app = false;
    bool chatgpt_web = false;
    bool wechat = false;
    bool wechat_existing = false;
    bool tun_debug_log = false;
    std::wstring antigravity_path;
    std::wstring cursor_path;
    std::wstring browser_path;
    std::wstring wechat_path;
    std::wstring tun_engine_path;
    std::wstring windivert_engine_path;
    easy_net::windivert::Backend wechat_backend = easy_net::windivert::Backend::tun;
    easy_net::tun::UdpMode tun_udp_mode = easy_net::tun::UdpMode::automatic;
    easy_net::tun::Stack tun_stack = easy_net::tun::Stack::system;
    bool tun_stack_explicit = false;
    std::vector<std::string> tun_bypass;
    std::wstring app_user_model_id;
    std::optional<DWORD> process_id;
    std::vector<std::wstring> command;
};

void PrintUsage() {
    std::wcerr
        << L"Easy-Net Hook - launch one Windows application through a SOCKS5 proxy\n\n"
        << L"Usage:\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 [--dns DNS] [options] -- app.exe [args...]\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 [--dns DNS] [options] --pid PID\n\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 [--dns DNS] [options] --appx AUMID\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 --antigravity [options] [-- app-args...]\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 --cursor [options] [-- app-args...]\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 --chatgpt-app [options]\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 --chatgpt-web [options]\n\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 --wechat [options] [-- app-args...]\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 --wechat-existing [options]\n\n"
        << L"Options:\n"
        << L"  --gui                  Open the graphical launcher (also the default with no arguments)\n"
        << L"  --username VALUE       SOCKS5 username (optional)\n"
        << L"  --password VALUE       SOCKS5 password (optional, max 255 bytes)\n"
        << L"  --dns IP[:PORT]        Use a specific DNS server (default: Windows DNS)\n"
        << L"  --no-children          Do not inject the hook into child processes\n"
        << L"  --allow-udp-direct     Allow UDP to bypass the proxy (may leak traffic)\n"
        << L"  --pid PID              Inject into an already running process\n"
        << L"  --appx AUMID           Activate a packaged desktop app, then inject it\n"
        << L"  --antigravity          Open Antigravity IDE and its language server through SOCKS5\n"
        << L"  --antigravity-path P   Antigravity IDE executable (optional)\n"
        << L"  --antigravity-isolated Use a separate profile instead of the normal login state\n"
        << L"  --cursor               Open Cursor through native Chromium SOCKS5\n"
        << L"  --cursor-path PATH     Cursor.exe executable (optional, auto-detected)\n"
        << L"  --cursor-isolated      Use a separate Cursor profile instead of normal login state\n"
        << L"  --chatgpt-app          Open the installed ChatGPT app with native Chromium SOCKS5\n"
        << L"  --chatgpt-web          Open ChatGPT in an isolated Edge/Chrome SOCKS5 session\n"
        << L"  --browser-path PATH    Browser executable for --chatgpt-web (optional)\n"
        << L"  --wechat               Open WeChat/Weixin through per-process routing\n"
        << L"  --wechat-existing      Attach routing to running WeChat/Weixin\n"
        << L"  --wechat-path PATH     WeChat.exe or Weixin.exe (optional, auto-detected)\n"
        << L"  --wechat-backend MODE  tun or windivert (default: tun)\n"
        << L"  --tun-engine PATH      sing-box.exe used by WeChat TUN modes (optional)\n"
        << L"  --windivert-engine P   easy-net-windivert.exe path (optional)\n"
        << L"  --tun-udp MODE         auto, proxy, block, or direct (default: auto)\n"
        << L"  --tun-stack MODE       system, mixed, or gvisor (default: system)\n"
        << L"  --tun-bypass CIDR      Bypass TUN for a CIDR; repeat or comma-separate values\n"
        << L"  --tun-debug-log        Log each network connection for temporary diagnostics\n"
        << L"  --wechat-status        Show WinDivert supervisor health and exit nonzero if unhealthy\n"
        << L"  --detach               Exit after the target process starts\n"
        << L"  --help                  Show this help\n\n"
        << L"The proxy host must be a literal IPv4 address or a bracketed IPv6 address.\n";
}

bool ParseOptions(int argc, wchar_t** argv, Options& options) {
    bool command_started = false;
    for (int index = 1; index < argc; ++index) {
        const std::wstring argument = argv[index];
        if (command_started) {
            options.command.push_back(argument);
            continue;
        }
        if (argument == L"--") {
            command_started = true;
        } else if (argument == L"--proxy" || argument == L"--username" ||
                   argument == L"--password" || argument == L"--dns" ||
                   argument == L"--pid" || argument == L"--browser-path" ||
                   argument == L"--antigravity-path" || argument == L"--cursor-path" ||
                   argument == L"--wechat-path" ||
                   argument == L"--tun-engine" || argument == L"--windivert-engine" ||
                   argument == L"--wechat-backend" || argument == L"--tun-udp" ||
                   argument == L"--tun-stack" || argument == L"--tun-bypass" ||
                   argument == L"--appx") {
            if (++index >= argc) {
                std::wcerr << L"Missing value for " << argument << L".\n";
                return false;
            }
            if (argument == L"--pid") {
                wchar_t* end = nullptr;
                const unsigned long value = std::wcstoul(argv[index], &end, 10);
                if (value == 0 || end == argv[index] || end == nullptr || *end != L'\0') {
                    std::wcerr << L"Invalid process ID: " << argv[index] << L".\n";
                    return false;
                }
                options.process_id = static_cast<DWORD>(value);
            } else if (argument == L"--browser-path") {
                options.browser_path = argv[index];
            } else if (argument == L"--antigravity-path") {
                options.antigravity_path = argv[index];
            } else if (argument == L"--cursor-path") {
                options.cursor_path = argv[index];
            } else if (argument == L"--wechat-path") {
                options.wechat_path = argv[index];
            } else if (argument == L"--tun-engine") {
                options.tun_engine_path = argv[index];
            } else if (argument == L"--windivert-engine") {
                options.windivert_engine_path = argv[index];
            } else if (argument == L"--wechat-backend") {
                const auto value = ToUtf8(argv[index]);
                if (!value || !easy_net::windivert::ParseBackend(*value, options.wechat_backend)) {
                    std::wcerr << L"Invalid --wechat-backend value. Use tun or windivert.\n";
                    return false;
                }
            } else if (argument == L"--tun-udp") {
                const auto value = ToUtf8(argv[index]);
                if (!value || !easy_net::tun::ParseUdpMode(*value, options.tun_udp_mode)) {
                    std::wcerr << L"Invalid --tun-udp value. Use auto, proxy, block, or direct.\n";
                    return false;
                }
            } else if (argument == L"--tun-stack") {
                const auto value = ToUtf8(argv[index]);
                if (!value || !easy_net::tun::ParseStack(*value, options.tun_stack)) {
                    std::wcerr << L"Invalid --tun-stack value. Use system, mixed, or gvisor.\n";
                    return false;
                }
                options.tun_stack_explicit = true;
            } else if (argument == L"--tun-bypass") {
                const auto value = ToUtf8(argv[index]);
                if (!value ||
                    !easy_net::tun::AppendRouteExclusions(*value, options.tun_bypass)) {
                    std::wcerr << L"Invalid --tun-bypass value. Use an IPv4 or IPv6 CIDR.\n";
                    return false;
                }
            } else if (argument == L"--appx") {
                options.app_user_model_id = argv[index];
            } else if (argument == L"--proxy") {
                options.proxy = argv[index];
            } else if (argument == L"--username") {
                options.username = argv[index];
            } else if (argument == L"--dns") {
                options.dns = argv[index];
            } else {
                options.password = argv[index];
            }
        } else if (argument == L"--no-children") {
            options.inject_children = false;
        } else if (argument == L"--allow-udp-direct") {
            options.allow_udp_direct = true;
        } else if (argument == L"--antigravity") {
            options.antigravity = true;
        } else if (argument == L"--antigravity-isolated") {
            options.antigravity_isolated = true;
        } else if (argument == L"--cursor") {
            options.cursor = true;
        } else if (argument == L"--cursor-isolated") {
            options.cursor_isolated = true;
        } else if (argument == L"--chatgpt-app") {
            options.chatgpt_app = true;
        } else if (argument == L"--chatgpt-web") {
            options.chatgpt_web = true;
        } else if (argument == L"--wechat") {
            options.wechat = true;
        } else if (argument == L"--wechat-existing") {
            options.wechat = true;
            options.wechat_existing = true;
        } else if (argument == L"--tun-debug-log") {
            options.tun_debug_log = true;
        } else if (argument == L"--detach") {
            options.detach = true;
        } else if (argument == L"--gui-worker") {
            // Internal GUI flag: the target must not inherit the launcher's
            // temporary stdout/stderr diagnostic pipe.
            options.gui_worker = true;
        } else if (argument == L"--help" || argument == L"-h" || argument == L"/?") {
            PrintUsage();
            ExitProcess(0);
        } else {
            std::wcerr << L"Unknown option: " << argument << L"\n";
            return false;
        }
    }

    if (options.proxy.empty()) {
        std::wcerr << L"--proxy is required.\n";
        return false;
    }
    const int target_count = ((!options.command.empty() && !options.antigravity && !options.cursor && !options.wechat) ? 1 : 0) +
                              (options.process_id.has_value() ? 1 : 0) +
                              (!options.app_user_model_id.empty() ? 1 : 0) +
                              (options.antigravity ? 1 : 0) +
                              (options.cursor ? 1 : 0) +
                              (options.chatgpt_app ? 1 : 0) +
                              (options.chatgpt_web ? 1 : 0) +
                              (options.wechat ? 1 : 0);
    if (target_count != 1) {
        std::wcerr << L"Specify exactly one target: a command after --, --pid PID, "
                      L"--appx AUMID, --antigravity, --chatgpt-app, --chatgpt-web, --wechat, "
                      L"--cursor, or --wechat-existing.\n";
        return false;
    }
    if (!options.browser_path.empty() && !options.chatgpt_web) {
        std::wcerr << L"--browser-path can only be used with --chatgpt-web.\n";
        return false;
    }
    if (!options.antigravity_path.empty() && !options.antigravity) {
        std::wcerr << L"--antigravity-path can only be used with --antigravity.\n";
        return false;
    }
    if (options.antigravity_isolated && !options.antigravity) {
        std::wcerr << L"--antigravity-isolated can only be used with --antigravity.\n";
        return false;
    }
    if (!options.cursor_path.empty() && !options.cursor) {
        std::wcerr << L"--cursor-path can only be used with --cursor.\n";
        return false;
    }
    if (options.cursor_isolated && !options.cursor) {
        std::wcerr << L"--cursor-isolated can only be used with --cursor.\n";
        return false;
    }
    if (!options.wechat_path.empty() && !options.wechat) {
        std::wcerr << L"--wechat-path can only be used with --wechat.\n";
        return false;
    }
    if (options.wechat_existing && !options.wechat_path.empty()) {
        std::wcerr << L"--wechat-path is not used with --wechat-existing.\n";
        return false;
    }
    if (options.wechat_existing && !options.command.empty()) {
        std::wcerr << L"Application arguments cannot be used with --wechat-existing.\n";
        return false;
    }
    if ((!options.tun_engine_path.empty() || !options.windivert_engine_path.empty() ||
         options.wechat_backend != easy_net::windivert::Backend::tun ||
         options.tun_udp_mode != easy_net::tun::UdpMode::automatic ||
         options.tun_stack_explicit || !options.tun_bypass.empty() || options.tun_debug_log) &&
        !options.wechat) {
        std::wcerr << L"WeChat network backend options can only be used with --wechat or "
                      L"--wechat-existing.\n";
        return false;
    }
    if (options.wechat_backend == easy_net::windivert::Backend::windivert &&
        options.tun_stack_explicit) {
        std::wcerr << L"--tun-stack applies only to --wechat-backend tun.\n";
        return false;
    }
    return true;
}

std::wstring QuoteCommandLineArgument(const std::wstring& argument) {
    if (argument.empty()) {
        return L"\"\"";
    }
    if (argument.find_first_of(L" \t\n\v\"") == std::wstring::npos) {
        return argument;
    }

    std::wstring quoted = L"\"";
    std::size_t backslashes = 0;
    for (const wchar_t character : argument) {
        if (character == L'\\') {
            ++backslashes;
            continue;
        }
        if (character == L'\"') {
            quoted.append(backslashes * 2 + 1, L'\\');
            quoted.push_back(L'\"');
        } else {
            quoted.append(backslashes, L'\\');
            quoted.push_back(character);
        }
        backslashes = 0;
    }
    quoted.append(backslashes * 2, L'\\');
    quoted.push_back(L'\"');
    return quoted;
}

std::wstring BuildCommandLine(const std::vector<std::wstring>& command) {
    std::wstring result;
    for (std::size_t index = 0; index < command.size(); ++index) {
        if (index != 0) {
            result.push_back(L' ');
        }
        result += QuoteCommandLineArgument(command[index]);
    }
    return result;
}

std::optional<std::wstring> CurrentModuleDirectory() {
    std::vector<wchar_t> buffer(32768);
    const DWORD length = GetModuleFileNameW(nullptr, buffer.data(), static_cast<DWORD>(buffer.size()));
    if (length == 0 || length >= buffer.size()) {
        return std::nullopt;
    }
    return std::filesystem::path(std::wstring(buffer.data(), length)).parent_path().wstring();
}

std::optional<std::string> ToDetoursPath(const std::wstring& path) {
    const int size = WideCharToMultiByte(CP_ACP, WC_NO_BEST_FIT_CHARS, path.c_str(), -1,
                                         nullptr, 0, nullptr, nullptr);
    if (size <= 1) {
        return std::nullopt;
    }
    std::string result(static_cast<std::size_t>(size), '\0');
    BOOL used_default = FALSE;
    if (WideCharToMultiByte(CP_ACP, WC_NO_BEST_FIT_CHARS, path.c_str(), -1,
                            result.data(), size, nullptr, &used_default) == 0 || used_default) {
        return std::nullopt;
    }
    result.resize(static_cast<std::size_t>(size - 1));
    return result;
}

bool SetConfigEnvironment(const Options& options) {
    return SetEnvironmentVariableW(L"EASY_NET_HOOK_PROXY", options.proxy.c_str()) &&
           SetEnvironmentVariableW(L"EASY_NET_HOOK_USERNAME", options.username.c_str()) &&
           SetEnvironmentVariableW(L"EASY_NET_HOOK_PASSWORD", options.password.c_str()) &&
           SetEnvironmentVariableW(L"EASY_NET_HOOK_DNS", options.dns.c_str()) &&
           SetEnvironmentVariableW(L"EASY_NET_HOOK_CHILDREN", options.inject_children ? L"1" : L"0") &&
           SetEnvironmentVariableW(L"EASY_NET_HOOK_ALLOW_UDP_DIRECT", options.allow_udp_direct ? L"1" : L"0");
}

bool BuildConfigBlock(const Options& options, easy_net::ipc::ConfigBlock& block) {
    block.inject_children = options.inject_children ? 1U : 0U;
    block.allow_udp_direct = options.allow_udp_direct ? 1U : 0U;
    return easy_net::ipc::CopyString(block.proxy, options.proxy) &&
           easy_net::ipc::CopyString(block.username, options.username) &&
           easy_net::ipc::CopyString(block.password, options.password) &&
           easy_net::ipc::CopyString(block.dns, options.dns);
}

class ScopedHandle {
public:
    explicit ScopedHandle(HANDLE handle = nullptr) : handle_(handle) {}
    ~ScopedHandle() {
        if (handle_ != nullptr && handle_ != INVALID_HANDLE_VALUE) {
            CloseHandle(handle_);
        }
    }
    ScopedHandle(const ScopedHandle&) = delete;
    ScopedHandle& operator=(const ScopedHandle&) = delete;
    ScopedHandle(ScopedHandle&& other) noexcept : handle_(other.release()) {}
    ScopedHandle& operator=(ScopedHandle&& other) noexcept {
        if (this != &other) {
            if (handle_ != nullptr && handle_ != INVALID_HANDLE_VALUE) {
                CloseHandle(handle_);
            }
            handle_ = other.release();
        }
        return *this;
    }
    HANDLE get() const { return handle_; }
    HANDLE release() {
        const HANDLE result = handle_;
        handle_ = nullptr;
        return result;
    }

private:
    HANDLE handle_;
};

std::optional<std::wstring> EnvironmentValue(const wchar_t* name) {
    const DWORD required = GetEnvironmentVariableW(name, nullptr, 0);
    if (required == 0) {
        return std::nullopt;
    }
    std::wstring value(static_cast<std::size_t>(required), L'\0');
    const DWORD written = GetEnvironmentVariableW(name, value.data(), required);
    if (written == 0 || written >= required) {
        return std::nullopt;
    }
    value.resize(written);
    return value;
}

std::optional<std::filesystem::path> FindChromiumBrowser(const Options& options) {
    if (!options.browser_path.empty()) {
        const std::filesystem::path configured(options.browser_path);
        if (std::filesystem::is_regular_file(configured)) {
            return configured;
        }
        return std::nullopt;
    }

    std::vector<std::filesystem::path> candidates;
    const auto program_files_x86 = EnvironmentValue(L"ProgramFiles(x86)");
    const auto program_files = EnvironmentValue(L"ProgramFiles");
    const auto local_app_data = EnvironmentValue(L"LOCALAPPDATA");
    if (program_files_x86) {
        candidates.emplace_back(std::filesystem::path(*program_files_x86) /
                                L"Microsoft/Edge/Application/msedge.exe");
        candidates.emplace_back(std::filesystem::path(*program_files_x86) /
                                L"Google/Chrome/Application/chrome.exe");
    }
    if (program_files) {
        candidates.emplace_back(std::filesystem::path(*program_files) /
                                L"Microsoft/Edge/Application/msedge.exe");
        candidates.emplace_back(std::filesystem::path(*program_files) /
                                L"Google/Chrome/Application/chrome.exe");
    }
    if (local_app_data) {
        candidates.emplace_back(std::filesystem::path(*local_app_data) /
                                L"Microsoft/Edge/Application/msedge.exe");
        candidates.emplace_back(std::filesystem::path(*local_app_data) /
                                L"Google/Chrome/Application/chrome.exe");
    }
    for (const auto& candidate : candidates) {
        if (std::filesystem::is_regular_file(candidate)) {
            return candidate;
        }
    }
    return std::nullopt;
}

std::optional<std::filesystem::path> FindChatGptExecutable() {
    constexpr wchar_t package_family[] = L"OpenAI.Codex_2p2nqsd0c76g0";
    UINT32 count = 0;
    UINT32 buffer_length = 0;
    LONG result = GetPackagesByPackageFamily(package_family, &count, nullptr,
                                             &buffer_length, nullptr);
    if (result != ERROR_INSUFFICIENT_BUFFER || count == 0 || buffer_length == 0) {
        return std::nullopt;
    }

    std::vector<PWSTR> package_names(count);
    std::vector<wchar_t> name_buffer(buffer_length);
    result = GetPackagesByPackageFamily(package_family, &count, package_names.data(),
                                        &buffer_length, name_buffer.data());
    if (result != ERROR_SUCCESS) {
        return std::nullopt;
    }

    for (UINT32 index = 0; index < count; ++index) {
        UINT32 path_length = 0;
        result = GetStagedPackagePathByFullName(package_names[index], &path_length, nullptr);
        if (result != ERROR_INSUFFICIENT_BUFFER || path_length == 0) {
            continue;
        }
        std::vector<wchar_t> path_buffer(path_length);
        result = GetStagedPackagePathByFullName(package_names[index], &path_length,
                                                path_buffer.data());
        if (result != ERROR_SUCCESS) {
            continue;
        }
        const std::filesystem::path executable =
            std::filesystem::path(path_buffer.data()) / L"app/ChatGPT.exe";
        if (std::filesystem::is_regular_file(executable)) {
            return executable;
        }
    }
    return std::nullopt;
}

std::optional<std::filesystem::path> FindRunningExecutable(const wchar_t* executable_name) {
    ScopedHandle snapshot(CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0));
    if (snapshot.get() == INVALID_HANDLE_VALUE) {
        return std::nullopt;
    }

    PROCESSENTRY32W entry{};
    entry.dwSize = sizeof(entry);
    if (!Process32FirstW(snapshot.get(), &entry)) {
        return std::nullopt;
    }
    do {
        if (_wcsicmp(entry.szExeFile, executable_name) != 0) {
            continue;
        }
        ScopedHandle process(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE,
                                         entry.th32ProcessID));
        if (process.get() == nullptr) {
            continue;
        }
        std::vector<wchar_t> path(32768);
        DWORD length = static_cast<DWORD>(path.size());
        if (QueryFullProcessImageNameW(process.get(), 0, path.data(), &length)) {
            const std::filesystem::path candidate(std::wstring(path.data(), length));
            if (std::filesystem::is_regular_file(candidate)) {
                return candidate;
            }
        }
    } while (Process32NextW(snapshot.get(), &entry));
    return std::nullopt;
}

std::optional<std::filesystem::path> FindAntigravityExecutable(const Options& options) {
    if (!options.antigravity_path.empty()) {
        const std::filesystem::path configured(options.antigravity_path);
        if (std::filesystem::is_regular_file(configured)) {
            return configured;
        }
        return std::nullopt;
    }
    if (const auto running = FindRunningExecutable(L"Antigravity IDE.exe")) {
        return running;
    }

    std::vector<std::filesystem::path> candidates;
    const auto local_app_data = EnvironmentValue(L"LOCALAPPDATA");
    const auto program_files = EnvironmentValue(L"ProgramFiles");
    const auto program_files_x86 = EnvironmentValue(L"ProgramFiles(x86)");
    const auto add_candidates = [&candidates](const std::wstring& root) {
        const std::filesystem::path base = std::filesystem::path(root) / L"Antigravity IDE";
        candidates.emplace_back(base / L"Antigravity IDE.exe");
        candidates.emplace_back(base / L"_" / L"Antigravity IDE.exe");
    };
    if (local_app_data) {
        add_candidates((std::filesystem::path(*local_app_data) / L"Programs").wstring());
    }
    if (program_files) {
        add_candidates(*program_files);
    }
    if (program_files_x86) {
        add_candidates(*program_files_x86);
    }
    for (const auto& candidate : candidates) {
        if (std::filesystem::is_regular_file(candidate)) {
            return candidate;
        }
    }
    return std::nullopt;
}

std::optional<std::filesystem::path> FindCursorExecutable(const Options& options) {
    if (!options.cursor_path.empty()) {
        const std::filesystem::path configured(options.cursor_path);
        if (std::filesystem::is_regular_file(configured)) {
            return configured;
        }
        return std::nullopt;
    }
    if (const auto running = FindRunningExecutable(L"Cursor.exe")) {
        return running;
    }

    std::vector<std::filesystem::path> candidates;
    const auto local_app_data = EnvironmentValue(L"LOCALAPPDATA");
    const auto program_files = EnvironmentValue(L"ProgramFiles");
    const auto program_files_x86 = EnvironmentValue(L"ProgramFiles(x86)");
    if (local_app_data) {
        candidates.emplace_back(std::filesystem::path(*local_app_data) /
                                L"Programs/cursor/Cursor.exe");
        candidates.emplace_back(std::filesystem::path(*local_app_data) /
                                L"Programs/Cursor/Cursor.exe");
    }
    if (program_files) {
        candidates.emplace_back(std::filesystem::path(*program_files) / L"Cursor/Cursor.exe");
    }
    if (program_files_x86) {
        candidates.emplace_back(std::filesystem::path(*program_files_x86) /
                                L"Cursor/Cursor.exe");
    }
    for (const auto& candidate : candidates) {
        if (std::filesystem::is_regular_file(candidate)) {
            return candidate;
        }
    }
    return std::nullopt;
}

bool IsWeChatMainProcessName(const wchar_t* name) {
    return _wcsicmp(name, L"Weixin.exe") == 0 || _wcsicmp(name, L"WeChat.exe") == 0 ||
           _wcsicmp(name, L"xwechat.exe") == 0;
}

bool AnyWeChatMainProcessRunning() {
    ScopedHandle snapshot(CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0));
    if (snapshot.get() == INVALID_HANDLE_VALUE) {
        return false;
    }
    PROCESSENTRY32W entry{};
    entry.dwSize = sizeof(entry);
    if (!Process32FirstW(snapshot.get(), &entry)) {
        return false;
    }
    do {
        if (IsWeChatMainProcessName(entry.szExeFile)) {
            return true;
        }
    } while (Process32NextW(snapshot.get(), &entry));
    return false;
}

struct RunningWeChatProcess {
    DWORD process_id = 0;
    std::wstring name;
};

std::optional<RunningWeChatProcess> FindRunningWeChatProcess() {
    ScopedHandle snapshot(CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0));
    if (snapshot.get() == INVALID_HANDLE_VALUE) {
        return std::nullopt;
    }
    PROCESSENTRY32W entry{};
    entry.dwSize = sizeof(entry);
    if (!Process32FirstW(snapshot.get(), &entry)) {
        return std::nullopt;
    }
    do {
        if (IsWeChatMainProcessName(entry.szExeFile)) {
            return RunningWeChatProcess{entry.th32ProcessID, entry.szExeFile};
        }
    } while (Process32NextW(snapshot.get(), &entry));
    return std::nullopt;
}

std::optional<std::wstring> RegistryString(HKEY root, const wchar_t* key,
                                           const wchar_t* name) {
    DWORD bytes = 0;
    const LSTATUS query = RegGetValueW(root, key, name, RRF_RT_REG_SZ, nullptr, nullptr, &bytes);
    if (query != ERROR_SUCCESS || bytes < sizeof(wchar_t)) {
        return std::nullopt;
    }
    std::wstring value(bytes / sizeof(wchar_t), L'\0');
    if (RegGetValueW(root, key, name, RRF_RT_REG_SZ, nullptr, value.data(), &bytes) !=
        ERROR_SUCCESS) {
        return std::nullopt;
    }
    while (!value.empty() && value.back() == L'\0') {
        value.pop_back();
    }
    return value;
}

std::optional<std::filesystem::path> FindWeChatExecutable(const Options& options) {
    if (!options.wechat_path.empty()) {
        const std::filesystem::path configured(options.wechat_path);
        if (std::filesystem::is_regular_file(configured)) {
            return configured;
        }
        return std::nullopt;
    }
    for (const wchar_t* name : {L"Weixin.exe", L"WeChat.exe"}) {
        if (const auto running = FindRunningExecutable(name)) {
            return running;
        }
    }

    std::vector<std::filesystem::path> candidates;
    const auto add_install_path = [&candidates](const std::optional<std::wstring>& value) {
        if (!value || value->empty()) {
            return;
        }
        const std::filesystem::path base(*value);
        candidates.emplace_back(base / L"Weixin.exe");
        candidates.emplace_back(base / L"WeChat.exe");
    };
    add_install_path(RegistryString(HKEY_CURRENT_USER, L"Software\\Tencent\\WeChat", L"InstallPath"));
    add_install_path(RegistryString(HKEY_CURRENT_USER, L"Software\\Tencent\\Weixin", L"InstallPath"));
    add_install_path(RegistryString(HKEY_LOCAL_MACHINE, L"Software\\Tencent\\WeChat", L"InstallPath"));
    add_install_path(RegistryString(HKEY_LOCAL_MACHINE, L"Software\\WOW6432Node\\Tencent\\WeChat", L"InstallPath"));

    const auto add_root = [&candidates](const std::optional<std::wstring>& root) {
        if (!root) {
            return;
        }
        const std::filesystem::path base(*root);
        candidates.emplace_back(base / L"Tencent/Weixin/Weixin.exe");
        candidates.emplace_back(base / L"Tencent/WeChat/WeChat.exe");
        candidates.emplace_back(base / L"Weixin/Weixin.exe");
        candidates.emplace_back(base / L"WeChat/WeChat.exe");
    };
    add_root(EnvironmentValue(L"ProgramFiles"));
    add_root(EnvironmentValue(L"ProgramFiles(x86)"));
    add_root(EnvironmentValue(L"LOCALAPPDATA"));
    for (const auto& candidate : candidates) {
        std::error_code error;
        if (std::filesystem::is_regular_file(candidate, error)) {
            return candidate;
        }
    }
    return std::nullopt;
}

std::optional<std::filesystem::path> FindTunEngine(const Options& options) {
    if (!options.tun_engine_path.empty()) {
        const std::filesystem::path configured(options.tun_engine_path);
        if (std::filesystem::is_regular_file(configured)) {
            return configured;
        }
        return std::nullopt;
    }
    if (const auto directory = CurrentModuleDirectory()) {
        for (const auto& candidate : {
                 std::filesystem::path(*directory) / L"tun/sing-box.exe",
                 std::filesystem::path(*directory) / L"sing-box.exe",
             }) {
            if (std::filesystem::is_regular_file(candidate)) {
                return candidate;
            }
        }
    }
    std::vector<wchar_t> found(32768);
    const DWORD length = SearchPathW(nullptr, L"sing-box.exe", nullptr,
                                     static_cast<DWORD>(found.size()), found.data(), nullptr);
    if (length > 0 && length < found.size()) {
        return std::filesystem::path(std::wstring(found.data(), length));
    }
    return std::nullopt;
}

std::optional<std::filesystem::path> FindWinDivertEngine(const Options& options) {
    if (!options.windivert_engine_path.empty()) {
        const std::filesystem::path configured(options.windivert_engine_path);
        if (std::filesystem::is_regular_file(configured)) {
            return configured;
        }
        return std::nullopt;
    }
    if (const auto directory = CurrentModuleDirectory()) {
        for (const auto& candidate : {
                 std::filesystem::path(*directory) / L"windivert/easy-net-windivert.exe",
                 std::filesystem::path(*directory) / L"easy-net-windivert.exe",
             }) {
            if (std::filesystem::is_regular_file(candidate)) {
                return candidate;
            }
        }
    }
    return std::nullopt;
}

bool IsProcessElevated() {
    ScopedHandle token;
    HANDLE raw_token = nullptr;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &raw_token)) {
        return false;
    }
    token = ScopedHandle(raw_token);
    TOKEN_ELEVATION elevation{};
    DWORD returned = 0;
    return GetTokenInformation(token.get(), TokenElevation, &elevation, sizeof(elevation),
                               &returned) && elevation.TokenIsElevated != 0;
}

int RelaunchElevated() {
    int argument_count = 0;
    LPWSTR* arguments = CommandLineToArgvW(GetCommandLineW(), &argument_count);
    if (arguments == nullptr || argument_count < 2) {
        if (arguments != nullptr) {
            LocalFree(arguments);
        }
        return 5;
    }
    std::vector<std::wstring> parameters;
    for (int index = 1; index < argument_count; ++index) {
        parameters.emplace_back(arguments[index]);
    }
    const std::wstring executable(arguments[0]);
    LocalFree(arguments);
    const std::wstring parameter_line = BuildCommandLine(parameters);

    SHELLEXECUTEINFOW execute{};
    execute.cbSize = sizeof(execute);
    execute.fMask = SEE_MASK_NOCLOSEPROCESS | SEE_MASK_FLAG_NO_UI;
    execute.lpVerb = L"runas";
    execute.lpFile = executable.c_str();
    execute.lpParameters = parameter_line.c_str();
    execute.nShow = SW_HIDE;
    if (!ShellExecuteExW(&execute)) {
        std::wcerr << L"Administrator permission is required for TUN mode (error "
                   << GetLastError() << L").\n";
        return 5;
    }
    ScopedHandle elevated(execute.hProcess);
    // Preserve the elevated launcher's actual result. The GUI waits for this hidden worker
    // asynchronously, so a slow UAC prompt no longer needs to be reported as success.
    const DWORD wait = WaitForSingleObject(elevated.get(), INFINITE);
    if (wait == WAIT_OBJECT_0) {
        DWORD exit_code = 5;
        if (GetExitCodeProcess(elevated.get(), &exit_code)) {
            return static_cast<int>(exit_code);
        }
    }
    return 5;
}

bool SendAll(SOCKET socket, const std::uint8_t* data, std::size_t size) {
    std::size_t sent = 0;
    while (sent < size) {
        const int count = send(socket, reinterpret_cast<const char*>(data + sent),
                               static_cast<int>(size - sent), 0);
        if (count <= 0) {
            return false;
        }
        sent += static_cast<std::size_t>(count);
    }
    return true;
}

bool ReceiveAll(SOCKET socket, std::uint8_t* data, std::size_t size) {
    std::size_t received = 0;
    while (received < size) {
        const int count = recv(socket, reinterpret_cast<char*>(data + received),
                               static_cast<int>(size - received), 0);
        if (count <= 0) {
            return false;
        }
        received += static_cast<std::size_t>(count);
    }
    return true;
}

bool Socks5SupportsUdp(const Options& options, const easy_net::tun::Endpoint& endpoint) {
    WSADATA winsock{};
    if (WSAStartup(MAKEWORD(2, 2), &winsock) != 0) {
        return false;
    }
    const auto cleanup = [] { WSACleanup(); };
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
        cleanup();
        return false;
    }
    const SOCKET socket = ::socket(family, SOCK_STREAM, IPPROTO_TCP);
    if (socket == INVALID_SOCKET) {
        cleanup();
        return false;
    }
    const DWORD timeout = 2000;
    setsockopt(socket, SOL_SOCKET, SO_RCVTIMEO, reinterpret_cast<const char*>(&timeout),
               sizeof(timeout));
    setsockopt(socket, SOL_SOCKET, SO_SNDTIMEO, reinterpret_cast<const char*>(&timeout),
               sizeof(timeout));
    bool supported = false;
    do {
        if (connect(socket, reinterpret_cast<const sockaddr*>(&address), address_length) != 0) {
            break;
        }
        const bool has_credentials = !options.username.empty() || !options.password.empty();
        const std::uint8_t greeting_with_auth[]{5, 2, 0, 2};
        const std::uint8_t greeting_without_auth[]{5, 1, 0};
        if (!SendAll(socket, has_credentials ? greeting_with_auth : greeting_without_auth,
                     has_credentials ? sizeof(greeting_with_auth) : sizeof(greeting_without_auth))) {
            break;
        }
        std::uint8_t greeting_reply[2]{};
        if (!ReceiveAll(socket, greeting_reply, sizeof(greeting_reply)) || greeting_reply[0] != 5 ||
            greeting_reply[1] == 0xff) {
            break;
        }
        if (greeting_reply[1] == 2) {
            const auto username = ToUtf8(options.username);
            const auto password = ToUtf8(options.password);
            if (!username || !password || username->size() > 255 || password->size() > 255) {
                break;
            }
            std::vector<std::uint8_t> auth{1, static_cast<std::uint8_t>(username->size())};
            auth.insert(auth.end(), username->begin(), username->end());
            auth.push_back(static_cast<std::uint8_t>(password->size()));
            auth.insert(auth.end(), password->begin(), password->end());
            std::uint8_t auth_reply[2]{};
            if (!SendAll(socket, auth.data(), auth.size()) ||
                !ReceiveAll(socket, auth_reply, sizeof(auth_reply)) || auth_reply[1] != 0) {
                break;
            }
        } else if (greeting_reply[1] != 0) {
            break;
        }
        const std::uint8_t udp_associate[]{5, 3, 0, 1, 0, 0, 0, 0, 0, 0};
        std::uint8_t reply[4]{};
        if (SendAll(socket, udp_associate, sizeof(udp_associate)) &&
            ReceiveAll(socket, reply, sizeof(reply)) && reply[0] == 5 && reply[1] == 0) {
            supported = true;
        }
    } while (false);
    closesocket(socket);
    cleanup();
    return supported;
}

bool Socks5EndpointResponsive(const easy_net::tun::Endpoint& endpoint) {
    return easy_net::socks5_health::Responsive(endpoint);
}

bool WriteUtf8File(const std::filesystem::path& path, const std::string& content) {
    ScopedHandle file(CreateFileW(path.c_str(), GENERIC_WRITE, FILE_SHARE_READ, nullptr,
                                  CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr));
    if (file.get() == INVALID_HANDLE_VALUE) {
        return false;
    }
    DWORD written = 0;
    return content.size() <= MAXDWORD &&
           WriteFile(file.get(), content.data(), static_cast<DWORD>(content.size()), &written,
                     nullptr) && written == content.size();
}

std::optional<std::string> ReadUtf8File(const std::filesystem::path& path,
                                        DWORD maximum_bytes = 64 * 1024) {
    ScopedHandle file(CreateFileW(path.c_str(), GENERIC_READ,
                                  FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                                  nullptr, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, nullptr));
    if (file.get() == INVALID_HANDLE_VALUE) {
        return std::nullopt;
    }
    LARGE_INTEGER size{};
    if (!GetFileSizeEx(file.get(), &size) || size.QuadPart < 0 ||
        size.QuadPart > maximum_bytes) {
        return std::nullopt;
    }
    std::string content(static_cast<std::size_t>(size.QuadPart), '\0');
    DWORD received = 0;
    if (!content.empty() &&
        (!ReadFile(file.get(), content.data(), static_cast<DWORD>(content.size()), &received,
                   nullptr) || received != static_cast<DWORD>(content.size()))) {
        return std::nullopt;
    }
    return content;
}

std::string UtcTimestamp() {
    SYSTEMTIME time{};
    GetSystemTime(&time);
    char value[32]{};
    snprintf(value, sizeof(value), "%04u-%02u-%02uT%02u:%02u:%02uZ",
             static_cast<unsigned int>(time.wYear),
             static_cast<unsigned int>(time.wMonth),
             static_cast<unsigned int>(time.wDay),
             static_cast<unsigned int>(time.wHour),
             static_cast<unsigned int>(time.wMinute),
             static_cast<unsigned int>(time.wSecond));
    return value;
}

bool WriteRuntimeStatus(const std::filesystem::path& path,
                        const easy_net::wechat::RuntimeStatus& status) {
    const std::filesystem::path temporary = path.wstring() + L".tmp";
    if (!WriteUtf8File(temporary, easy_net::wechat::BuildRuntimeStatus(status))) {
        return false;
    }
    if (!MoveFileExW(temporary.c_str(), path.c_str(),
                     MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH)) {
        DeleteFileW(temporary.c_str());
        return false;
    }
    return true;
}

bool StartHiddenProcess(const std::filesystem::path& executable,
                        const std::vector<std::wstring>& arguments, HANDLE output,
                        PROCESS_INFORMATION& process, bool inherit_additional_handles = false) {
    std::vector<std::wstring> command{executable.wstring()};
    command.insert(command.end(), arguments.begin(), arguments.end());
    std::wstring command_line = BuildCommandLine(command);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');
    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    BOOL inherit_handles = inherit_additional_handles ? TRUE : FALSE;
    if (output != nullptr && output != INVALID_HANDLE_VALUE) {
        startup.dwFlags = STARTF_USESTDHANDLES;
        startup.hStdOutput = output;
        startup.hStdError = output;
        startup.hStdInput = GetStdHandle(STD_INPUT_HANDLE);
        inherit_handles = TRUE;
    }
    return CreateProcessW(executable.c_str(), mutable_command.data(), nullptr, nullptr,
                          inherit_handles, CREATE_NO_WINDOW | CREATE_DEFAULT_ERROR_MODE, nullptr,
                          executable.parent_path().c_str(), &startup, &process) != FALSE;
}

constexpr LONGLONG kTunLogMaxBytes = 8LL * 1024 * 1024;

void RelayBoundedTunLog(HANDLE pipe, const std::filesystem::path& log_path) {
    ScopedHandle input(pipe);
    ScopedHandle output(CreateFileW(log_path.c_str(), GENERIC_WRITE,
                                    FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                                    nullptr, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr));
    LARGE_INTEGER log_size{};
    if (output.get() != INVALID_HANDLE_VALUE) {
        GetFileSizeEx(output.get(), &log_size);
        LARGE_INTEGER end{};
        SetFilePointerEx(output.get(), end, nullptr, FILE_END);
    }

    std::vector<char> buffer(64 * 1024);
    for (;;) {
        DWORD received = 0;
        if (!ReadFile(input.get(), buffer.data(), static_cast<DWORD>(buffer.size()), &received,
                      nullptr) || received == 0) {
            break;
        }
        if (output.get() == INVALID_HANDLE_VALUE) {
            continue;
        }
        if (log_size.QuadPart + received > kTunLogMaxBytes) {
            LARGE_INTEGER start{};
            if (SetFilePointerEx(output.get(), start, nullptr, FILE_BEGIN) &&
                SetEndOfFile(output.get())) {
                log_size.QuadPart = 0;
                constexpr char marker[] =
                    "--- Easy-Net Hook truncated the network log at 8 MiB ---\r\n";
                DWORD marker_written = 0;
                if (WriteFile(output.get(), marker, sizeof(marker) - 1, &marker_written,
                              nullptr)) {
                    log_size.QuadPart += marker_written;
                }
            }
        }
        DWORD offset = 0;
        while (offset < received) {
            DWORD written = 0;
            if (!WriteFile(output.get(), buffer.data() + offset, received - offset, &written,
                           nullptr) || written == 0) {
                break;
            }
            offset += written;
            log_size.QuadPart += written;
        }
    }
}

struct WeChatSupervisorConfig {
    bool restart_windivert = false;
    bool debug_log = false;
    std::filesystem::path engine_path;
    std::filesystem::path config_path;
    std::filesystem::path status_path;
    std::wstring proxy_text;
    easy_net::tun::Endpoint proxy;
};

int WatchWeChat(DWORD root_process_id, DWORD engine_process_id, HANDLE log_pipe = nullptr,
                const std::filesystem::path& log_path = {},
                const WeChatSupervisorConfig& supervisor = {}) {
    ScopedHandle root(OpenProcess(SYNCHRONIZE, FALSE, root_process_id));
    ScopedHandle engine(OpenProcess(SYNCHRONIZE | PROCESS_TERMINATE, FALSE, engine_process_id));
    if (engine.get() == nullptr) {
        if (log_pipe != nullptr && log_pipe != INVALID_HANDLE_VALUE) {
            CloseHandle(log_pipe);
        }
        return 2;
    }
    std::thread log_relay;
    if (log_pipe != nullptr && log_pipe != INVALID_HANDLE_VALUE && !log_path.empty()) {
        log_relay = std::thread(RelayBoundedTunLog, log_pipe, log_path);
    }
    const auto finish_log_relay = [&log_relay] {
        if (log_relay.joinable()) {
            CancelSynchronousIo(log_relay.native_handle());
            log_relay.join();
        }
    };
    easy_net::wechat::RuntimeStatus status;
    status.backend = supervisor.restart_windivert ? "windivert" : "tun";
    status.state = easy_net::wechat::HealthState::starting;
    status.message = "WeChat network engine is starting";
    status.proxy = ToUtf8(supervisor.proxy_text).value_or("");
    status.engine_pid = engine_process_id;
    status.fail_closed = supervisor.restart_windivert;
    status.heartbeat_tick_ms = GetTickCount64();
    status.updated_at = UtcTimestamp();
    if (!supervisor.status_path.empty()) {
        WriteRuntimeStatus(supervisor.status_path, status);
    }

    const auto update_status = [&](easy_net::wechat::HealthState state,
                                   std::string message, bool fail_closed) {
        status.state = state;
        status.message = std::move(message);
        status.fail_closed = fail_closed;
        status.heartbeat_tick_ms = GetTickCount64();
        status.updated_at = UtcTimestamp();
        if (!supervisor.status_path.empty()) {
            WriteRuntimeStatus(supervisor.status_path, status);
        }
    };
    const auto start_windivert = [&]() -> bool {
        SECURITY_ATTRIBUTES security{sizeof(security), nullptr, TRUE};
        HANDLE raw_read = nullptr;
        HANDLE raw_write = nullptr;
        if (!CreatePipe(&raw_read, &raw_write, &security, 64 * 1024)) {
            return false;
        }
        ScopedHandle read_pipe(raw_read);
        ScopedHandle write_pipe(raw_write);
        SetHandleInformation(read_pipe.get(), HANDLE_FLAG_INHERIT, 0);
        PROCESS_INFORMATION process{};
        const std::vector<std::wstring> arguments{
            L"--profile", supervisor.config_path.wstring(), L"--verbose",
            supervisor.debug_log ? L"3" : L"1",
        };
        if (!StartHiddenProcess(supervisor.engine_path, arguments, write_pipe.get(), process)) {
            return false;
        }
        CloseHandle(process.hThread);
        SetHandleInformation(write_pipe.get(), HANDLE_FLAG_INHERIT, 0);
        CloseHandle(write_pipe.release());
        engine = ScopedHandle(process.hProcess);
        status.engine_pid = process.dwProcessId;
        log_relay = std::thread(RelayBoundedTunLog, read_pipe.release(), log_path);
        if (WaitForSingleObject(engine.get(), 1500) == WAIT_OBJECT_0) {
            finish_log_relay();
            engine = ScopedHandle();
            status.engine_pid = 0;
            return false;
        }
        return true;
    };

    unsigned int empty_checks = 0;
    unsigned int proxy_failures = 0;
    unsigned int restart_failures = 0;
    constexpr unsigned int kHealthIntervalTicks = 40;
    unsigned int health_ticks = kHealthIntervalTicks;
    for (;;) {
        if (AnyWeChatMainProcessRunning()) {
            empty_checks = 0;
        } else if (++empty_checks >= 12) {
            if (engine.get() != nullptr &&
                WaitForSingleObject(engine.get(), 0) == WAIT_TIMEOUT) {
                TerminateProcess(engine.get(), 0);
                WaitForSingleObject(engine.get(), 5000);
            }
            finish_log_relay();
            status.engine_pid = 0;
            update_status(easy_net::wechat::HealthState::stopped,
                          "WeChat exited; network engine stopped", false);
            return 0;
        }

        if (engine.get() == nullptr || WaitForSingleObject(engine.get(), 0) != WAIT_TIMEOUT) {
            finish_log_relay();
            engine = ScopedHandle();
            status.engine_pid = 0;
            if (!supervisor.restart_windivert || supervisor.engine_path.empty() ||
                supervisor.config_path.empty()) {
                update_status(easy_net::wechat::HealthState::stopped,
                              "Network engine exited", false);
                return 5;
            }
            ++status.restart_count;
            update_status(easy_net::wechat::HealthState::restarting,
                          "WinDivert engine exited; automatic restart pending", false);
            const unsigned int backoff_seconds =
                1U << std::min(restart_failures, 5U);
            for (unsigned int elapsed = 0; elapsed < backoff_seconds * 4; ++elapsed) {
                if (!AnyWeChatMainProcessRunning()) {
                    break;
                }
                Sleep(250);
            }
            if (!AnyWeChatMainProcessRunning()) {
                continue;
            }
            if (!start_windivert()) {
                ++restart_failures;
                update_status(easy_net::wechat::HealthState::restart_failed,
                              "WinDivert restart failed; retrying with backoff", false);
                Sleep(250);
                continue;
            }
            restart_failures = 0;
            proxy_failures = 0;
            health_ticks = kHealthIntervalTicks;
            update_status(easy_net::wechat::HealthState::starting,
                          "WinDivert restarted; checking SOCKS5", true);
        }

        if (supervisor.restart_windivert && ++health_ticks >= kHealthIntervalTicks) {
            health_ticks = 0;
            if (Socks5EndpointResponsive(supervisor.proxy)) {
                proxy_failures = 0;
                update_status(easy_net::wechat::HealthState::healthy,
                              "WinDivert is running and SOCKS5 is responsive", true);
            } else if (++proxy_failures >= 2) {
                update_status(easy_net::wechat::HealthState::proxy_unavailable,
                              "SOCKS5 is unavailable; matching WeChat traffic remains fail-closed",
                              true);
            }
        }
        Sleep(250);
    }
}

int LaunchWeChat(const Options& options) {
#if !defined(_WIN64)
    std::wcerr << L"--wechat TUN/WinDivert modes are available only in the x64 package.\n";
    return 3;
#else
    std::wstring proxy_host;
    if (!easy_net::browser::ParseLiteralSocksEndpoint(options.proxy, proxy_host)) {
        std::wcerr << L"--wechat requires a literal SOCKS5 address such as 127.0.0.1:1080.\n";
        return 2;
    }
    if (!options.dns.empty()) {
        easy_net::dns::Endpoint parsed_dns;
        if (!easy_net::dns::ParseEndpoint(options.dns, parsed_dns)) {
            std::wcerr << L"Invalid --dns value. Use a literal IP address with an optional port.\n";
            return 2;
        }
    }
    std::optional<std::filesystem::path> executable;
    std::optional<RunningWeChatProcess> existing_process;
    if (options.wechat_existing) {
        existing_process = FindRunningWeChatProcess();
        if (!existing_process) {
            std::wcerr << L"No running Weixin.exe, WeChat.exe, or xwechat.exe was found. "
                          L"Start WeChat first, then use --wechat-existing.\n";
            return 6;
        }
    } else {
        executable = FindWeChatExecutable(options);
        if (!executable) {
            std::wcerr << L"WeChat.exe or Weixin.exe was not found. Use --wechat-path PATH.\n";
            return 3;
        }
        // Orphaned browser/update helpers must not prevent a fresh main WeChat instance.
        if (AnyWeChatMainProcessRunning()) {
            std::wcerr << L"WeChat is already running. Use --wechat-existing to attach TUN "
                          L"routing, or exit it completely before using --wechat.\n";
            return 6;
        }
    }
    const bool use_windivert =
        options.wechat_backend == easy_net::windivert::Backend::windivert;
    if (use_windivert && !options.dns.empty()) {
        std::wcerr << L"Warning: --dns is not applied by the WinDivert backend; Windows DNS "
                      L"remains in use.\n";
    }
    const auto engine = use_windivert ? FindWinDivertEngine(options) : FindTunEngine(options);
    if (!engine) {
        if (use_windivert) {
            std::wcerr << L"The WinDivert engine was not found. Use the x64-TUN package or "
                          L"specify --windivert-engine PATH.\n";
        } else {
            std::wcerr << L"The optional TUN engine was not found. Use the x64-TUN package, "
                          L"place sing-box.exe in the tun folder, or specify --tun-engine PATH.\n";
        }
        return 3;
    }
    if (!IsProcessElevated()) {
        return RelaunchElevated();
    }

    const auto proxy_text = ToUtf8(options.proxy);
    easy_net::tun::Endpoint proxy_endpoint;
    if (!proxy_text || !easy_net::tun::ParseEndpoint(*proxy_text, proxy_endpoint)) {
        return 2;
    }
    easy_net::tun::UdpMode udp_mode = options.tun_udp_mode;
    if (udp_mode == easy_net::tun::UdpMode::automatic) {
        udp_mode = Socks5SupportsUdp(options, proxy_endpoint) ? easy_net::tun::UdpMode::proxy
                                                              : easy_net::tun::UdpMode::block;
        std::wcout << (udp_mode == easy_net::tun::UdpMode::proxy
                           ? L"SOCKS5 UDP ASSOCIATE is available; WeChat UDP/QUIC will be proxied.\n"
                           : L"SOCKS5 UDP ASSOCIATE is unavailable; WeChat UDP is blocked to prevent leaks and TCP fallback will be used where possible.\n");
    }

    const auto local_app_data = EnvironmentValue(L"LOCALAPPDATA");
    if (!local_app_data) {
        std::wcerr << L"LOCALAPPDATA is unavailable; cannot prepare WeChat routing configuration.\n";
        return 3;
    }
    const std::filesystem::path config_directory =
        std::filesystem::path(*local_app_data) /
        (use_windivert ? L"EasyNetHook/WinDivert" : L"EasyNetHook/Tun");
    std::error_code directory_error;
    std::filesystem::create_directories(config_directory, directory_error);
    if (directory_error) {
        std::wcerr << L"Cannot create the WeChat routing configuration directory.\n";
        return 3;
    }
    const std::filesystem::path config_path =
        config_directory / (use_windivert ? L"wechat.pbprofile" : L"wechat.json");
    const std::filesystem::path log_path =
        config_directory / (use_windivert ? L"wechat-windivert.log" : L"wechat-tun.log");
    const std::filesystem::path status_path =
        config_directory / L"wechat-status.json";
    easy_net::tun::Config config;
    config.proxy = proxy_endpoint;
    config.interface_name = "easy-net-wechat-" + std::to_string(GetCurrentProcessId());
    const auto username = ToUtf8(options.username);
    const auto password = ToUtf8(options.password);
    if (!username || !password) {
        std::wcerr << L"Proxy credentials are not valid UTF-8.\n";
        return 2;
    }
    config.username = *username;
    config.password = *password;
    config.udp_mode = udp_mode;
    config.stack = options.tun_stack;
    for (const auto& prefix : options.tun_bypass) {
        if (std::find(config.route_exclude_addresses.begin(),
                      config.route_exclude_addresses.end(), prefix) ==
            config.route_exclude_addresses.end()) {
            config.route_exclude_addresses.push_back(prefix);
        }
    }
    config.log_level = options.tun_debug_log ? "info" : "warn";
    if (!options.dns.empty()) {
        const auto dns_text = ToUtf8(options.dns);
        easy_net::tun::Endpoint dns_endpoint;
        if (!dns_text || !easy_net::tun::ParseEndpoint(*dns_text, dns_endpoint, 53)) {
            return 2;
        }
        config.dns_host = dns_endpoint.host;
        config.dns_port = dns_endpoint.port;
    }
    std::string generated_config;
    if (use_windivert) {
        easy_net::windivert::Profile profile;
        profile.proxy = proxy_endpoint;
        profile.username = *username;
        profile.password = *password;
        profile.udp_mode = udp_mode;
        profile.traffic_logging = options.tun_debug_log;
        profile.bypass_prefixes = config.route_exclude_addresses;
        generated_config = easy_net::windivert::BuildProfile(profile);
    } else {
        generated_config = easy_net::tun::BuildConfig(config);
    }
    if (!WriteUtf8File(config_path, generated_config)) {
        std::wcerr << L"Cannot write WeChat routing configuration: "
                   << config_path.wstring() << L".\n";
        return 4;
    }

    SECURITY_ATTRIBUTES security{sizeof(security), nullptr, TRUE};
    if (!use_windivert) {
        ScopedHandle validation_log(CreateFileW(
            log_path.c_str(), GENERIC_WRITE,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, &security, CREATE_ALWAYS,
            FILE_ATTRIBUTE_NORMAL, nullptr));
        if (validation_log.get() == INVALID_HANDLE_VALUE) {
            std::wcerr << L"Cannot open TUN log: " << log_path.wstring() << L".\n";
            return 4;
        }
        PROCESS_INFORMATION checker{};
        if (!StartHiddenProcess(*engine, {L"check", L"-c", config_path.wstring()},
                                validation_log.get(), checker)) {
            std::wcerr << L"Cannot validate the TUN configuration (error " << GetLastError()
                       << L").\n";
            return 5;
        }
        CloseHandle(checker.hThread);
        const DWORD check_wait = WaitForSingleObject(checker.hProcess, 10000);
        DWORD check_exit = 1;
        if (check_wait != WAIT_OBJECT_0) {
            TerminateProcess(checker.hProcess, 5);
            WaitForSingleObject(checker.hProcess, 5000);
        } else {
            GetExitCodeProcess(checker.hProcess, &check_exit);
        }
        CloseHandle(checker.hProcess);
        if (check_wait != WAIT_OBJECT_0 || check_exit != 0) {
            std::wcerr << L"TUN configuration validation failed. See " << log_path.wstring()
                       << L".\n";
            return 5;
        }
    }

    HANDLE raw_log_read = nullptr;
    HANDLE raw_log_write = nullptr;
    if (!CreatePipe(&raw_log_read, &raw_log_write, &security, 64 * 1024)) {
        std::wcerr << L"Cannot create the bounded TUN log pipe (error " << GetLastError()
                   << L").\n";
        return 4;
    }
    ScopedHandle log_read(raw_log_read);
    ScopedHandle log_write(raw_log_write);
    SetHandleInformation(log_read.get(), HANDLE_FLAG_INHERIT, 0);

    PROCESS_INFORMATION tun_process{};
    const std::vector<std::wstring> engine_arguments = use_windivert
        ? std::vector<std::wstring>{L"--profile", config_path.wstring(), L"--verbose",
                                    options.tun_debug_log ? L"3" : L"1"}
        : std::vector<std::wstring>{L"run", L"-c", config_path.wstring()};
    if (!StartHiddenProcess(*engine, engine_arguments, log_write.get(), tun_process)) {
        std::wcerr << L"Cannot start the WeChat network engine (error " << GetLastError()
                   << L").\n";
        return 5;
    }
    CloseHandle(tun_process.hThread);
    SetHandleInformation(log_write.get(), HANDLE_FLAG_INHERIT, 0);
    if (WaitForSingleObject(tun_process.hProcess, 1500) == WAIT_OBJECT_0) {
        DWORD engine_exit = 1;
        GetExitCodeProcess(tun_process.hProcess, &engine_exit);
        CloseHandle(tun_process.hProcess);
        CloseHandle(log_write.release());
        RelayBoundedTunLog(log_read.release(), log_path);
        std::wcerr << (use_windivert ? L"The WinDivert engine exited with code "
                                     : L"The TUN engine exited with code ")
                   << engine_exit << L". See "
                   << log_path.wstring() << L".\n";
        return 5;
    }

    PROCESS_INFORMATION wechat_process{};
    if (options.wechat_existing) {
        wechat_process.dwProcessId = existing_process->process_id;
        wechat_process.hProcess = OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION,
                                              FALSE, wechat_process.dwProcessId);
        if (wechat_process.hProcess == nullptr ||
            WaitForSingleObject(wechat_process.hProcess, 0) != WAIT_TIMEOUT) {
            const DWORD error = GetLastError();
            if (wechat_process.hProcess != nullptr) {
                CloseHandle(wechat_process.hProcess);
            }
            TerminateProcess(tun_process.hProcess, 5);
            WaitForSingleObject(tun_process.hProcess, 5000);
            CloseHandle(tun_process.hProcess);
            std::wcerr << L"The running WeChat process exited before TUN could attach (error "
                       << error << L").\n";
            return 6;
        }
        std::wcout << L"Attached " << (use_windivert ? L"WinDivert" : L"TUN")
                   << L" TCP/UDP routing and SOCKS5 " << options.proxy << L" to running "
                   << existing_process->name << L" (PID " << wechat_process.dwProcessId
                   << L"). New connections will use the selected route.\n";
    } else {
        std::vector<std::wstring> wechat_command{executable->wstring()};
        wechat_command.insert(wechat_command.end(), options.command.begin(), options.command.end());
        std::wstring command_line = BuildCommandLine(wechat_command);
        std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
        mutable_command.push_back(L'\0');
        STARTUPINFOW startup{};
        startup.cb = sizeof(startup);
        if (!CreateProcessW(executable->c_str(), mutable_command.data(), nullptr, nullptr, FALSE,
                            CREATE_DEFAULT_ERROR_MODE, nullptr, executable->parent_path().c_str(),
                            &startup, &wechat_process)) {
            const DWORD error = GetLastError();
            TerminateProcess(tun_process.hProcess, 5);
            WaitForSingleObject(tun_process.hProcess, 5000);
            CloseHandle(tun_process.hProcess);
            std::wcerr << L"Cannot start WeChat (error " << error << L").\n";
            return 5;
        }
        CloseHandle(wechat_process.hThread);
        wechat_process.hThread = nullptr;
        std::wcout << L"Opened " << executable->filename().wstring()
                   << L" through " << (use_windivert ? L"WinDivert" : L"TUN")
                   << L" and SOCKS5 " << options.proxy << L" (PID "
                   << wechat_process.dwProcessId << L").\n";
    }
    std::wcout << (use_windivert ? L"WinDivert log: " : L"TUN log: ")
               << log_path.wstring() << L"\n";
    if (use_windivert) {
        std::wcout << L"Supervisor status: " << status_path.wstring() << L"\n";
    }

    const auto launcher_directory = CurrentModuleDirectory();
    const std::filesystem::path launcher =
        launcher_directory ? std::filesystem::path(*launcher_directory) / L"easy-net-hook.exe"
                           : std::filesystem::path{};
    PROCESS_INFORMATION watcher{};
    const bool can_inherit_log =
        SetHandleInformation(log_read.get(), HANDLE_FLAG_INHERIT, HANDLE_FLAG_INHERIT) != FALSE;
    const std::wstring log_handle =
        std::to_wstring(reinterpret_cast<std::uintptr_t>(log_read.get()));
    std::vector<std::wstring> watcher_arguments{
        L"--wechat-watch", std::to_wstring(wechat_process.dwProcessId),
        std::to_wstring(tun_process.dwProcessId), log_handle, log_path.wstring(),
    };
    if (use_windivert) {
        watcher_arguments.insert(watcher_arguments.end(),
                                 {L"windivert", engine->wstring(), config_path.wstring(),
                                  options.proxy, options.tun_debug_log ? L"1" : L"0"});
    }
    const bool watcher_started = can_inherit_log && !launcher.empty() &&
        StartHiddenProcess(launcher, watcher_arguments, nullptr, watcher, true);
    SetHandleInformation(log_read.get(), HANDLE_FLAG_INHERIT, 0);
    CloseHandle(log_write.release());
    CloseHandle(log_read.release());
    if (!watcher_started) {
        if (!options.wechat_existing) {
            TerminateProcess(wechat_process.hProcess, 5);
        }
        TerminateProcess(tun_process.hProcess, 5);
        WaitForSingleObject(tun_process.hProcess, 5000);
        CloseHandle(wechat_process.hProcess);
        CloseHandle(tun_process.hProcess);
        std::wcerr << L"Cannot start the WeChat network lifecycle and log watcher.\n";
        return 5;
    }
    CloseHandle(watcher.hThread);
    CloseHandle(wechat_process.hProcess);
    CloseHandle(tun_process.hProcess);
    if (options.detach) {
        CloseHandle(watcher.hProcess);
        return 0;
    }
    WaitForSingleObject(watcher.hProcess, INFINITE);
    DWORD watcher_exit = 1;
    GetExitCodeProcess(watcher.hProcess, &watcher_exit);
    CloseHandle(watcher.hProcess);
    return static_cast<int>(watcher_exit);
#endif
}

bool SetNativeSocksEnvironment(const std::wstring& proxy, bool compatible_scheme = false) {
    for (const auto& [name, value] :
         easy_net::browser::NativeSocksEnvironment(proxy, compatible_scheme)) {
        if (!SetEnvironmentVariableW(name.c_str(), value.c_str())) {
            return false;
        }
    }
    return true;
}

std::wstring AntigravityWatcherEventName(DWORD process_id) {
    return L"Local\\EasyNetHook_AntigravityWatcher_" + std::to_wstring(process_id);
}

bool StartAntigravityWatcher(DWORD process_id, const Options& options) {
    std::vector<wchar_t> module_path(32768);
    const DWORD length = GetModuleFileNameW(nullptr, module_path.data(),
                                            static_cast<DWORD>(module_path.size()));
    if (length == 0 || length >= static_cast<DWORD>(module_path.size())) {
        return false;
    }
    const std::wstring executable(module_path.data(), length);
    ScopedHandle ready(CreateEventW(nullptr, TRUE, FALSE,
                                    AntigravityWatcherEventName(process_id).c_str()));
    if (ready.get() == nullptr) {
        return false;
    }

    std::vector<std::wstring> command{
        executable,
        L"--antigravity-watch",
        std::to_wstring(process_id),
        L"--proxy",
        options.proxy,
    };
    if (!options.dns.empty()) {
        command.insert(command.end(), {L"--dns", options.dns});
    }
    std::wstring command_line = BuildCommandLine(command);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');

    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    PROCESS_INFORMATION watcher{};
    if (!CreateProcessW(executable.c_str(), mutable_command.data(), nullptr, nullptr, FALSE,
                        CREATE_NO_WINDOW | CREATE_DEFAULT_ERROR_MODE, nullptr, nullptr,
                        &startup, &watcher)) {
        return false;
    }
    CloseHandle(watcher.hThread);
    const DWORD wait = WaitForSingleObject(ready.get(), 5000);
    if (wait != WAIT_OBJECT_0) {
        TerminateProcess(watcher.hProcess, 4);
        WaitForSingleObject(watcher.hProcess, 5000);
        CloseHandle(watcher.hProcess);
        return false;
    }
    CloseHandle(watcher.hProcess);
    return true;
}

std::wstring CursorWatcherReadyEventName(DWORD process_id) {
    return L"Local\\EasyNetHook_CursorWatcher_" + std::to_wstring(process_id);
}

std::wstring CursorManagedEventName(const Options& options) {
    if (options.cursor_isolated) {
        return L"Local\\EasyNetHook_Cursor_Isolated_" +
               easy_net::browser::ProfileKey(options.proxy);
    }
    return L"Local\\EasyNetHook_Cursor_Default";
}

std::wstring CursorProxyEventName(const std::wstring& proxy) {
    return L"Local\\EasyNetHook_Cursor_DefaultProxy_" +
           easy_net::browser::ProfileKey(proxy);
}

bool NamedEventExists(const std::wstring& name) {
    ScopedHandle event(OpenEventW(SYNCHRONIZE, FALSE, name.c_str()));
    return event.get() != nullptr;
}

bool StartCursorWatcher(DWORD process_id, const Options& options) {
    std::vector<wchar_t> module_path(32768);
    const DWORD length = GetModuleFileNameW(nullptr, module_path.data(),
                                            static_cast<DWORD>(module_path.size()));
    if (length == 0 || length >= static_cast<DWORD>(module_path.size())) {
        return false;
    }
    const std::wstring executable(module_path.data(), length);
    ScopedHandle ready(CreateEventW(nullptr, TRUE, FALSE,
                                    CursorWatcherReadyEventName(process_id).c_str()));
    if (ready.get() == nullptr) {
        return false;
    }

    std::vector<std::wstring> command{
        executable, L"--cursor-watch", std::to_wstring(process_id), L"--proxy", options.proxy,
    };
    if (options.cursor_isolated) {
        command.push_back(L"--isolated");
    }
    std::wstring command_line = BuildCommandLine(command);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');
    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    PROCESS_INFORMATION watcher{};
    if (!CreateProcessW(executable.c_str(), mutable_command.data(), nullptr, nullptr, FALSE,
                        CREATE_NO_WINDOW | CREATE_DEFAULT_ERROR_MODE, nullptr, nullptr,
                        &startup, &watcher)) {
        return false;
    }
    CloseHandle(watcher.hThread);
    const DWORD wait = WaitForSingleObject(ready.get(), 5000);
    if (wait != WAIT_OBJECT_0) {
        TerminateProcess(watcher.hProcess, 4);
        WaitForSingleObject(watcher.hProcess, 5000);
        CloseHandle(watcher.hProcess);
        return false;
    }
    CloseHandle(watcher.hProcess);
    return true;
}

int LaunchChatGptApp(const Options& options) {
    if (!options.username.empty() || !options.password.empty()) {
        std::wcerr << L"Chromium does not support SOCKS5 username/password authentication. "
                      L"Use a local unauthenticated SOCKS5 endpoint for --chatgpt-app.\n";
        return 2;
    }
    std::wstring proxy_host;
    if (!easy_net::browser::ParseLiteralSocksEndpoint(options.proxy, proxy_host)) {
        std::wcerr << L"--chatgpt-app requires a literal SOCKS5 address such as 127.0.0.1:1080.\n";
        return 2;
    }
    const auto executable = FindChatGptExecutable();
    if (!executable) {
        std::wcerr << L"The installed OpenAI.Codex package or app/ChatGPT.exe was not found.\n";
        return 3;
    }
    const auto local_app_data = EnvironmentValue(L"LOCALAPPDATA");
    if (!local_app_data) {
        std::wcerr << L"LOCALAPPDATA is unavailable; cannot create the isolated ChatGPT profile.\n";
        return 3;
    }
    const std::filesystem::path profile = std::filesystem::path(*local_app_data) /
                                          L"EasyNetHook/ChatGPTAppProfile" /
                                          easy_net::browser::ProfileKey(options.proxy);
    std::error_code directory_error;
    std::filesystem::create_directories(profile, directory_error);
    if (directory_error) {
        std::wcerr << L"Cannot create the ChatGPT profile: " << profile.wstring() << L".\n";
        return 3;
    }
    if (!SetEnvironmentVariableW(L"CODEX_ELECTRON_USER_DATA_PATH", profile.c_str())) {
        std::wcerr << L"Cannot prepare the isolated ChatGPT environment (error "
                   << GetLastError() << L").\n";
        return 4;
    }
    if (!SetNativeSocksEnvironment(options.proxy)) {
        std::wcerr << L"Cannot configure the ChatGPT backend proxy environment (error "
                   << GetLastError() << L").\n";
        return 4;
    }
    if (!options.dns.empty()) {
        std::wcerr << L"Note: --dns is ignored in --chatgpt-app mode; Chromium sends URL hostnames "
                      L"to the SOCKS5 proxy.\n";
    }

    std::vector<std::wstring> command{
        executable->wstring(),
        L"--user-data-dir=" + profile.wstring(),
    };
    const auto proxy_arguments = easy_net::browser::NativeSocksArguments(options.proxy, proxy_host);
    command.insert(command.end(), proxy_arguments.begin(), proxy_arguments.end());
    command.push_back(L"--no-first-run");

    std::wstring command_line = BuildCommandLine(command);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');

    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    PROCESS_INFORMATION process{};
    if (!CreateProcessW(executable->c_str(), mutable_command.data(), nullptr, nullptr, FALSE,
                        CREATE_DEFAULT_ERROR_MODE, nullptr, nullptr, &startup, &process)) {
        std::wcerr << L"Cannot start the ChatGPT app (error " << GetLastError() << L").\n";
        return 5;
    }
    CloseHandle(process.hThread);
    std::wcout << L"Opened the ChatGPT app and Codex backend through native SOCKS5 "
               << options.proxy << L" without DLL injection (PID " << process.dwProcessId
               << L").\n";
    if (options.detach) {
        CloseHandle(process.hProcess);
        return 0;
    }
    WaitForSingleObject(process.hProcess, INFINITE);
    DWORD exit_code = 1;
    GetExitCodeProcess(process.hProcess, &exit_code);
    CloseHandle(process.hProcess);
    return static_cast<int>(exit_code);
}

int LaunchAntigravity(const Options& options) {
#if !defined(_WIN64)
    std::wcerr << L"--antigravity requires the x64 Easy-Net Hook package because its language "
                  L"server is 64-bit.\n";
    return 3;
#endif
    if (!options.username.empty() || !options.password.empty()) {
        std::wcerr << L"Antigravity Chromium networking does not support SOCKS5 username/password "
                      L"authentication. Use a local unauthenticated SOCKS5 endpoint.\n";
        return 2;
    }
    std::wstring proxy_host;
    if (!easy_net::browser::ParseLiteralSocksEndpoint(options.proxy, proxy_host)) {
        std::wcerr << L"--antigravity requires a literal SOCKS5 address such as 127.0.0.1:1080.\n";
        return 2;
    }
    const auto executable = FindAntigravityExecutable(options);
    if (!executable) {
        std::wcerr << L"Antigravity IDE.exe was not found. Use --antigravity-path PATH.\n";
        return 3;
    }
    if (!options.antigravity_isolated && FindRunningExecutable(L"Antigravity IDE.exe")) {
        std::wcerr << L"Antigravity IDE is already running. Fully exit it first so the new "
                      L"default-profile process can inherit the proxy, or use "
                      L"--antigravity-isolated.\n";
        return 6;
    }

    std::optional<std::filesystem::path> profile;
    if (options.antigravity_isolated) {
        const auto local_app_data = EnvironmentValue(L"LOCALAPPDATA");
        if (!local_app_data) {
            std::wcerr << L"LOCALAPPDATA is unavailable; cannot create the Antigravity profile.\n";
            return 3;
        }
        profile = std::filesystem::path(*local_app_data) / L"EasyNetHook/AntigravityProfile" /
                  easy_net::browser::ProfileKey(options.proxy);
        std::error_code directory_error;
        std::filesystem::create_directories(*profile, directory_error);
        if (directory_error) {
            std::wcerr << L"Cannot create the Antigravity profile: " << profile->wstring()
                       << L".\n";
            return 3;
        }
    }
    if (!SetNativeSocksEnvironment(options.proxy, true)) {
        std::wcerr << L"Cannot configure the Antigravity proxy environment (error "
                   << GetLastError() << L").\n";
        return 4;
    }
    if (!options.dns.empty()) {
        easy_net::dns::Endpoint dns_server;
        if (!easy_net::dns::ParseEndpoint(options.dns, dns_server)) {
            std::wcerr << L"Invalid --dns value for the Antigravity language-server fallback.\n";
            return 2;
        }
    }

    std::vector<std::wstring> command{executable->wstring()};
    if (profile) {
        command.push_back(L"--user-data-dir=" + profile->wstring());
    }
    const auto proxy_arguments = easy_net::browser::NativeSocksArguments(options.proxy, proxy_host);
    command.insert(command.end(), proxy_arguments.begin(), proxy_arguments.end());
    command.push_back(L"--no-first-run");
    command.push_back(L"--reuse-window");
    command.insert(command.end(), options.command.begin(), options.command.end());

    std::wstring command_line = BuildCommandLine(command);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');
    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    PROCESS_INFORMATION process{};
    const std::wstring working_directory = executable->parent_path().wstring();
    if (!CreateProcessW(executable->c_str(), mutable_command.data(), nullptr, nullptr, FALSE,
                        CREATE_DEFAULT_ERROR_MODE | CREATE_SUSPENDED, nullptr,
                        working_directory.c_str(),
                        &startup, &process)) {
        std::wcerr << L"Cannot start Antigravity IDE (error " << GetLastError() << L").\n";
        return 5;
    }
    if (!StartAntigravityWatcher(process.dwProcessId, options)) {
        std::wcerr << L"Cannot start the Antigravity language-server monitor.\n";
        TerminateProcess(process.hProcess, 5);
        WaitForSingleObject(process.hProcess, 5000);
        CloseHandle(process.hThread);
        CloseHandle(process.hProcess);
        return 5;
    }
    if (ResumeThread(process.hThread) == static_cast<DWORD>(-1)) {
        std::wcerr << L"Cannot resume Antigravity IDE (error " << GetLastError() << L").\n";
        TerminateProcess(process.hProcess, 5);
        WaitForSingleObject(process.hProcess, 5000);
        CloseHandle(process.hThread);
        CloseHandle(process.hProcess);
        return 5;
    }
    CloseHandle(process.hThread);
    std::wcout << L"Opened Antigravity IDE through native SOCKS5 " << options.proxy
               << L" and enabled the language-server fallback Hook (PID "
               << process.dwProcessId << L", profile "
               << (options.antigravity_isolated ? L"isolated" : L"default") << L").\n";
    if (options.detach) {
        CloseHandle(process.hProcess);
        return 0;
    }
    WaitForSingleObject(process.hProcess, INFINITE);
    DWORD exit_code = 1;
    GetExitCodeProcess(process.hProcess, &exit_code);
    CloseHandle(process.hProcess);
    return static_cast<int>(exit_code);
}

int LaunchCursor(const Options& options) {
    if (!options.username.empty() || !options.password.empty()) {
        std::wcerr << L"Cursor Chromium networking does not support SOCKS5 username/password "
                      L"authentication. Use a local unauthenticated SOCKS5 endpoint.\n";
        return 2;
    }
    if (!options.dns.empty()) {
        std::wcerr << L"--dns is not supported by Cursor native proxy mode. DNS names are "
                      L"resolved through the SOCKS5 proxy automatically.\n";
        return 2;
    }
    std::wstring proxy_host;
    if (!easy_net::browser::ParseLiteralSocksEndpoint(options.proxy, proxy_host)) {
        std::wcerr << L"--cursor requires a literal SOCKS5 address such as 127.0.0.1:1080.\n";
        return 2;
    }
    const auto executable = FindCursorExecutable(options);
    if (!executable) {
        std::wcerr << L"Cursor.exe was not found. Use --cursor-path PATH.\n";
        return 3;
    }

    const bool cursor_running = FindRunningExecutable(L"Cursor.exe").has_value();
    const std::wstring managed_event = CursorManagedEventName(options);
    bool reuse_managed_instance = NamedEventExists(managed_event);
    if (!options.cursor_isolated && cursor_running) {
        const bool same_proxy = NamedEventExists(CursorProxyEventName(options.proxy));
        if (!reuse_managed_instance || !same_proxy) {
            std::wcerr << L"Cursor is already running without this Easy-Net proxy. Fully exit "
                          L"Cursor first, or use --cursor-isolated.\n";
            return 6;
        }
    }

    std::optional<std::filesystem::path> profile;
    if (options.cursor_isolated) {
        const auto local_app_data = EnvironmentValue(L"LOCALAPPDATA");
        if (!local_app_data) {
            std::wcerr << L"LOCALAPPDATA is unavailable; cannot create the Cursor profile.\n";
            return 3;
        }
        profile = std::filesystem::path(*local_app_data) / L"EasyNetHook/CursorProfile" /
                  easy_net::browser::ProfileKey(options.proxy);
        std::error_code directory_error;
        std::filesystem::create_directories(*profile, directory_error);
        if (directory_error) {
            std::wcerr << L"Cannot create the Cursor profile: " << profile->wstring() << L".\n";
            return 3;
        }
    }
    if (!SetNativeSocksEnvironment(options.proxy, true)) {
        std::wcerr << L"Cannot configure the Cursor proxy environment (error "
                   << GetLastError() << L").\n";
        return 4;
    }

    std::optional<std::string> detours_dll_path;
    if (!reuse_managed_instance) {
        const auto module_directory = CurrentModuleDirectory();
        if (!module_directory) {
            std::wcerr << L"Cannot locate the launcher directory (error " << GetLastError()
                       << L").\n";
            return 3;
        }
        const std::filesystem::path dll_path =
            std::filesystem::path(*module_directory) / L"easy-net-hook.dll";
        if (!std::filesystem::is_regular_file(dll_path)) {
            std::wcerr << L"Cursor Node service fallback DLL not found: " << dll_path.wstring()
                       << L"\n";
            return 3;
        }
        detours_dll_path = ToDetoursPath(dll_path.wstring());
        if (!detours_dll_path) {
            std::wcerr << L"The DLL path cannot be represented by the current Windows ANSI "
                          L"code page. Move the package to an ASCII-only path.\n";
            return 3;
        }
        if (!SetConfigEnvironment(options)) {
            std::wcerr << L"Cannot configure the Cursor Node service fallback (error "
                       << GetLastError() << L").\n";
            return 4;
        }
    }

    std::vector<std::wstring> command{executable->wstring()};
    if (profile) {
        command.push_back(L"--user-data-dir=" + profile->wstring());
    }
    const auto proxy_arguments = easy_net::browser::NativeSocksArguments(options.proxy, proxy_host);
    command.insert(command.end(), proxy_arguments.begin(), proxy_arguments.end());
    command.push_back(L"--no-first-run");
    command.push_back(L"--new-window");
    command.insert(command.end(), options.command.begin(), options.command.end());

    std::wstring command_line = BuildCommandLine(command);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');
    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    PROCESS_INFORMATION process{};
    const DWORD creation_flags = reuse_managed_instance
                                     ? CREATE_DEFAULT_ERROR_MODE
                                     : CREATE_DEFAULT_ERROR_MODE | CREATE_SUSPENDED;
    const std::wstring working_directory = executable->parent_path().wstring();
    const BOOL created = reuse_managed_instance
                             ? CreateProcessW(executable->c_str(), mutable_command.data(), nullptr,
                                              nullptr, FALSE, creation_flags, nullptr,
                                              working_directory.c_str(), &startup, &process)
                             : DetourCreateProcessWithDllExW(
                                   executable->c_str(), mutable_command.data(), nullptr, nullptr,
                                   options.gui_worker ? FALSE : TRUE, creation_flags, nullptr,
                                   working_directory.c_str(),
                                   &startup, &process, detours_dll_path->c_str(), nullptr);
    if (!created) {
        std::wcerr << L"Cannot start Cursor (error " << GetLastError() << L").\n";
        return 5;
    }
    if (!reuse_managed_instance) {
        if (!StartCursorWatcher(process.dwProcessId, options)) {
            std::wcerr << L"Cannot start the Cursor lifecycle monitor.\n";
            TerminateProcess(process.hProcess, 5);
            WaitForSingleObject(process.hProcess, 5000);
            CloseHandle(process.hThread);
            CloseHandle(process.hProcess);
            return 5;
        }
        if (ResumeThread(process.hThread) == static_cast<DWORD>(-1)) {
            std::wcerr << L"Cannot resume Cursor (error " << GetLastError() << L").\n";
            TerminateProcess(process.hProcess, 5);
            WaitForSingleObject(process.hProcess, 5000);
            CloseHandle(process.hThread);
            CloseHandle(process.hProcess);
            return 5;
        }
    }
    CloseHandle(process.hThread);
    std::wcout << (reuse_managed_instance ? L"Opened another Cursor window through "
                                          : L"Opened Cursor through ")
               << L"native SOCKS5 plus Node service fallback " << options.proxy << L" (PID "
               << process.dwProcessId
               << L", profile " << (options.cursor_isolated ? L"isolated" : L"default")
               << L").\n";
    if (options.detach || reuse_managed_instance) {
        CloseHandle(process.hProcess);
        return 0;
    }
    WaitForSingleObject(process.hProcess, INFINITE);
    DWORD exit_code = 1;
    GetExitCodeProcess(process.hProcess, &exit_code);
    CloseHandle(process.hProcess);
    return static_cast<int>(exit_code);
}

int LaunchChatGptWeb(const Options& options) {
    if (!options.username.empty() || !options.password.empty()) {
        std::wcerr << L"Chromium does not support SOCKS5 username/password authentication. "
                      L"Use a local unauthenticated SOCKS5 endpoint for --chatgpt-web.\n";
        return 2;
    }
    std::wstring proxy_host;
    if (!easy_net::browser::ParseLiteralSocksEndpoint(options.proxy, proxy_host)) {
        std::wcerr << L"--chatgpt-web requires a literal SOCKS5 address such as 127.0.0.1:1080.\n";
        return 2;
    }
    const auto browser = FindChromiumBrowser(options);
    if (!browser) {
        std::wcerr << L"Microsoft Edge or Google Chrome was not found. Use --browser-path PATH.\n";
        return 3;
    }
    const auto local_app_data = EnvironmentValue(L"LOCALAPPDATA");
    if (!local_app_data) {
        std::wcerr << L"LOCALAPPDATA is unavailable; cannot create the isolated browser profile.\n";
        return 3;
    }
    const std::filesystem::path profile = std::filesystem::path(*local_app_data) /
                                          L"EasyNetHook/ChatGPTProfile" /
                                          easy_net::browser::ProfileKey(options.proxy);
    std::error_code directory_error;
    std::filesystem::create_directories(profile, directory_error);
    if (directory_error) {
        std::wcerr << L"Cannot create the browser profile: " << profile.wstring() << L".\n";
        return 3;
    }
    if (!options.dns.empty()) {
        std::wcerr << L"Note: --dns is ignored in --chatgpt-web mode; Chromium sends URL hostnames "
                      L"to the SOCKS5 proxy.\n";
    }

    std::vector<std::wstring> command{
        browser->wstring(),
        L"--user-data-dir=" + profile.wstring(),
    };
    const auto proxy_arguments = easy_net::browser::NativeSocksArguments(options.proxy, proxy_host);
    command.insert(command.end(), proxy_arguments.begin(), proxy_arguments.end());
    command.insert(command.end(), {L"--disable-background-networking", L"--disable-component-update",
                                   L"--no-first-run", L"--no-default-browser-check",
                                   L"https://chatgpt.com/"});
    std::wstring command_line = BuildCommandLine(command);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');

    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    PROCESS_INFORMATION process{};
    if (!CreateProcessW(browser->c_str(), mutable_command.data(), nullptr, nullptr, FALSE,
                        CREATE_DEFAULT_ERROR_MODE, nullptr, nullptr, &startup, &process)) {
        std::wcerr << L"Cannot start the protected ChatGPT browser session (error "
                   << GetLastError() << L").\n";
        return 5;
    }
    CloseHandle(process.hThread);
    std::wcout << L"Opened ChatGPT through SOCKS5 " << options.proxy
               << L" without DLL injection (PID " << process.dwProcessId << L").\n";
    if (options.detach) {
        CloseHandle(process.hProcess);
        return 0;
    }
    WaitForSingleObject(process.hProcess, INFINITE);
    DWORD exit_code = 1;
    GetExitCodeProcess(process.hProcess, &exit_code);
    CloseHandle(process.hProcess);
    return static_cast<int>(exit_code);
}

bool SameArchitecture(HANDLE process, std::wstring& error) {
    using IsWow64Process2Fn = BOOL(WINAPI*)(HANDLE, USHORT*, USHORT*);
    const auto is_wow64_process2 = reinterpret_cast<IsWow64Process2Fn>(
        GetProcAddress(GetModuleHandleW(L"kernel32.dll"), "IsWow64Process2"));
    if (is_wow64_process2 != nullptr) {
        USHORT current_machine = IMAGE_FILE_MACHINE_UNKNOWN;
        USHORT current_native = IMAGE_FILE_MACHINE_UNKNOWN;
        USHORT target_machine = IMAGE_FILE_MACHINE_UNKNOWN;
        USHORT target_native = IMAGE_FILE_MACHINE_UNKNOWN;
        if (!is_wow64_process2(GetCurrentProcess(), &current_machine, &current_native) ||
            !is_wow64_process2(process, &target_machine, &target_native)) {
            error = L"Cannot inspect target architecture (error " +
                    std::to_wstring(GetLastError()) + L").";
            return false;
        }
        const USHORT current = current_machine == IMAGE_FILE_MACHINE_UNKNOWN ? current_native : current_machine;
        const USHORT target = target_machine == IMAGE_FILE_MACHINE_UNKNOWN ? target_native : target_machine;
        if (current != target) {
            error = L"Target architecture does not match this package.";
            return false;
        }
        if (target != IMAGE_FILE_MACHINE_I386 && target != IMAGE_FILE_MACHINE_AMD64) {
            error = L"Only x86 and x64 targets are supported.";
            return false;
        }
        return true;
    }

    BOOL current_wow64 = FALSE;
    BOOL target_wow64 = FALSE;
    if (!IsWow64Process(GetCurrentProcess(), &current_wow64) ||
        !IsWow64Process(process, &target_wow64)) {
        error = L"Cannot inspect target architecture (error " +
                std::to_wstring(GetLastError()) + L").";
        return false;
    }
    if (current_wow64 != target_wow64) {
        error = L"Target architecture does not match this package.";
        return false;
    }
    return true;
}

bool HasUnsupportedInjectionPolicy(HANDLE process, DWORD process_id) {
    bool unsupported = false;
    HANDLE token = nullptr;
    if (OpenProcessToken(process, TOKEN_QUERY, &token)) {
        DWORD is_app_container = 0;
        DWORD returned = 0;
        if (GetTokenInformation(token, TokenIsAppContainer, &is_app_container,
                                sizeof(is_app_container), &returned) &&
            is_app_container != 0) {
            std::wcerr << L"PID " << process_id
                       << L" runs in AppContainer; DLL injection and the loopback relay are not supported.\n";
            unsupported = true;
        }
        CloseHandle(token);
    }

    HANDLE policy_process = OpenProcess(PROCESS_QUERY_INFORMATION, FALSE, process_id);
    if (policy_process == nullptr) {
        policy_process = process;
    }
    const bool close_policy_process = policy_process != process;

    PROCESS_MITIGATION_BINARY_SIGNATURE_POLICY signature{};
    if (GetProcessMitigationPolicy(policy_process, ProcessSignaturePolicy,
                                   &signature, sizeof(signature)) &&
        (signature.MicrosoftSignedOnly != 0 || signature.StoreSignedOnly != 0 ||
         signature.MitigationOptIn != 0)) {
        std::wcerr << L"PID " << process_id
                   << L" rejects ordinary third-party DLLs through its binary-signature policy.\n";
        unsupported = true;
    }

    PROCESS_MITIGATION_DYNAMIC_CODE_POLICY dynamic_code{};
    if (GetProcessMitigationPolicy(policy_process, ProcessDynamicCodePolicy,
                                   &dynamic_code, sizeof(dynamic_code)) &&
        dynamic_code.ProhibitDynamicCode != 0) {
        std::wcerr << L"PID " << process_id
                   << L" prohibits dynamic code, so Detours cannot create hook trampolines.\n";
        unsupported = true;
    }
    if (close_policy_process) {
        CloseHandle(policy_process);
    }

    if (unsupported) {
        std::wcerr << L"Use --chatgpt-web for a protected, non-injecting SOCKS5 session.\n";
    }
    return unsupported;
}

std::optional<std::uintptr_t> RemoteModuleBase(DWORD process_id, const wchar_t* module_name) {
    ScopedHandle snapshot(CreateToolhelp32Snapshot(TH32CS_SNAPMODULE | TH32CS_SNAPMODULE32, process_id));
    if (snapshot.get() == INVALID_HANDLE_VALUE) {
        return std::nullopt;
    }
    MODULEENTRY32W module{};
    module.dwSize = sizeof(module);
    if (!Module32FirstW(snapshot.get(), &module)) {
        return std::nullopt;
    }
    do {
        if (_wcsicmp(module.szModule, module_name) == 0) {
            return reinterpret_cast<std::uintptr_t>(module.modBaseAddr);
        }
    } while (Module32NextW(snapshot.get(), &module));
    return std::nullopt;
}

bool IsModuleLoaded(DWORD process_id, const std::filesystem::path& dll_path) {
    ScopedHandle snapshot(CreateToolhelp32Snapshot(TH32CS_SNAPMODULE | TH32CS_SNAPMODULE32, process_id));
    if (snapshot.get() == INVALID_HANDLE_VALUE) {
        return false;
    }
    MODULEENTRY32W module{};
    module.dwSize = sizeof(module);
    if (!Module32FirstW(snapshot.get(), &module)) {
        return false;
    }
    do {
        if (_wcsicmp(module.szModule, dll_path.filename().c_str()) == 0) {
            return true;
        }
    } while (Module32NextW(snapshot.get(), &module));
    return false;
}

std::optional<ScopedHandle> CreateConfigMapping(DWORD process_id, const Options& options) {
    easy_net::ipc::ConfigBlock block;
    if (!BuildConfigBlock(options, block)) {
        std::wcerr << L"Proxy, DNS, username, or password is too long.\n";
        return std::nullopt;
    }
    const std::wstring name = easy_net::ipc::ConfigMappingName(process_id);
    ScopedHandle mapping(CreateFileMappingW(INVALID_HANDLE_VALUE, nullptr, PAGE_READWRITE, 0,
                                            sizeof(block), name.c_str()));
    if (mapping.get() == nullptr) {
        std::wcerr << L"Cannot create the configuration mapping (error "
                   << GetLastError() << L").\n";
        return std::nullopt;
    }
    void* view = MapViewOfFile(mapping.get(), FILE_MAP_WRITE, 0, 0, sizeof(block));
    if (view == nullptr) {
        std::wcerr << L"Cannot map the configuration (error " << GetLastError() << L").\n";
        return std::nullopt;
    }
    std::memcpy(view, &block, sizeof(block));
    UnmapViewOfFile(view);
    return mapping;
}

bool InjectRunningProcess(DWORD process_id,
                          const std::filesystem::path& dll_path,
                          const Options& options) {
    ScopedHandle query_process(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, process_id));
    if (query_process.get() == nullptr) {
        std::wcerr << L"Cannot inspect PID " << process_id << L" (error " << GetLastError()
                   << L"). The process may be protected.\n";
        return false;
    }
    std::wstring architecture_error;
    if (!SameArchitecture(query_process.get(), architecture_error)) {
        std::wcerr << architecture_error << L" Use the x86 package for a 32-bit target and x64 for x64.\n";
        return false;
    }
    if (HasUnsupportedInjectionPolicy(query_process.get(), process_id)) {
        return false;
    }

    constexpr DWORD access = PROCESS_CREATE_THREAD | PROCESS_QUERY_INFORMATION |
                             PROCESS_VM_OPERATION | PROCESS_VM_WRITE | PROCESS_VM_READ |
                             SYNCHRONIZE;
    ScopedHandle process(OpenProcess(access, FALSE, process_id));
    if (process.get() == nullptr) {
        std::wcerr << L"Cannot open PID " << process_id << L" (error " << GetLastError()
                   << L"). Try running the matching package as administrator.\n";
        return false;
    }
    if (IsModuleLoaded(process_id, dll_path)) {
        std::wcerr << L"The hook DLL is already loaded in PID " << process_id
                   << L"; restart that process to change its configuration.\n";
        return false;
    }

    auto mapping = CreateConfigMapping(process_id, options);
    if (!mapping) {
        return false;
    }

    const std::wstring dll = dll_path.wstring();
    const SIZE_T bytes = (dll.size() + 1) * sizeof(wchar_t);
    void* remote_path = VirtualAllocEx(process.get(), nullptr, bytes, MEM_COMMIT | MEM_RESERVE, PAGE_READWRITE);
    if (remote_path == nullptr) {
        std::wcerr << L"Cannot allocate target memory (error " << GetLastError() << L").\n";
        return false;
    }
    bool success = false;
    bool remote_path_can_be_freed = true;
    do {
        SIZE_T written = 0;
        if (!WriteProcessMemory(process.get(), remote_path, dll.c_str(), bytes, &written) ||
            written != bytes) {
            std::wcerr << L"Cannot write the DLL path to the target (error " << GetLastError() << L").\n";
            break;
        }
        HMODULE local_kernel32 = GetModuleHandleW(L"kernel32.dll");
        const FARPROC local_load_library = GetProcAddress(local_kernel32, "LoadLibraryW");
        HMODULE local_export_module = nullptr;
        if (local_kernel32 == nullptr || local_load_library == nullptr ||
            !GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS |
                                    GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                                reinterpret_cast<LPCWSTR>(local_load_library),
                                &local_export_module)) {
            std::wcerr << L"Cannot resolve LoadLibraryW in the target.\n";
            break;
        }
        wchar_t export_module_path[MAX_PATH]{};
        if (GetModuleFileNameW(local_export_module, export_module_path,
                               static_cast<DWORD>(std::size(export_module_path))) == 0) {
            std::wcerr << L"Cannot locate the LoadLibraryW module.\n";
            break;
        }
        const std::filesystem::path export_module(export_module_path);
        const auto remote_export_module = RemoteModuleBase(process_id,
                                                            export_module.filename().c_str());
        if (!remote_export_module) {
            std::wcerr << L"Cannot locate " << export_module.filename().wstring()
                       << L" in the target.\n";
            break;
        }
        const auto offset = reinterpret_cast<std::uintptr_t>(local_load_library) -
                            reinterpret_cast<std::uintptr_t>(local_export_module);
        const auto remote_load_library = reinterpret_cast<LPTHREAD_START_ROUTINE>(
            *remote_export_module + offset);
        ScopedHandle thread(CreateRemoteThread(process.get(), nullptr, 0, remote_load_library,
                                               remote_path, 0, nullptr));
        if (thread.get() == nullptr) {
            std::wcerr << L"Cannot start the injection thread (error " << GetLastError() << L").\n";
            break;
        }
        const DWORD wait = WaitForSingleObject(thread.get(), 15000);
        if (wait != WAIT_OBJECT_0) {
            remote_path_can_be_freed = false;
            std::wcerr << L"The target injection thread did not finish within 15 seconds (wait="
                       << wait << L").\n";
            break;
        }
        if (!IsModuleLoaded(process_id, dll_path)) {
            std::wcerr << L"The target did not load the hook DLL. Windows may have blocked injection.\n";
            break;
        }
        success = true;
    } while (false);
    if (remote_path_can_be_freed) {
        VirtualFreeEx(process.get(), remote_path, 0, MEM_RELEASE);
    }
    if (success) {
        std::wcout << L"Attached PID " << process_id << L" through SOCKS5 " << options.proxy << L".\n";
    }
    return success;
}

bool IsDescendantProcess(DWORD process_id, DWORD root_process_id,
                         const std::unordered_map<DWORD, DWORD>& parents) {
    DWORD current = process_id;
    for (int depth = 0; depth < 64; ++depth) {
        const auto parent = parents.find(current);
        if (parent == parents.end() || parent->second == 0 || parent->second == current) {
            return false;
        }
        current = parent->second;
        if (current == root_process_id) {
            return true;
        }
    }
    return false;
}

int WatchAntigravityLanguageServer(DWORD root_process_id, const std::wstring& proxy,
                                   const std::wstring& dns) {
    std::wstring proxy_host;
    if (!easy_net::browser::ParseLiteralSocksEndpoint(proxy, proxy_host)) {
        return 2;
    }
    (void)proxy_host;
    if (!dns.empty()) {
        easy_net::dns::Endpoint dns_server;
        if (!easy_net::dns::ParseEndpoint(dns, dns_server)) {
            return 2;
        }
    }
    const auto module_directory = CurrentModuleDirectory();
    if (!module_directory) {
        return 3;
    }
    const std::filesystem::path dll_path =
        std::filesystem::path(*module_directory) / L"easy-net-hook.dll";
    if (!std::filesystem::is_regular_file(dll_path)) {
        return 3;
    }
    ScopedHandle root_process(OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION,
                                          FALSE, root_process_id));
    if (root_process.get() == nullptr) {
        return 4;
    }
    ScopedHandle ready(OpenEventW(EVENT_MODIFY_STATE, FALSE,
                                  AntigravityWatcherEventName(root_process_id).c_str()));
    if (ready.get() == nullptr || !SetEvent(ready.get())) {
        return 4;
    }

    Options options;
    options.proxy = proxy;
    options.dns = dns;
    options.inject_children = false;
    std::unordered_map<DWORD, unsigned int> attempts;
    while (WaitForSingleObject(root_process.get(), 0) == WAIT_TIMEOUT) {
        ScopedHandle snapshot(CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0));
        if (snapshot.get() != INVALID_HANDLE_VALUE) {
            std::vector<PROCESSENTRY32W> processes;
            std::unordered_map<DWORD, DWORD> parents;
            PROCESSENTRY32W entry{};
            entry.dwSize = sizeof(entry);
            if (Process32FirstW(snapshot.get(), &entry)) {
                do {
                    processes.push_back(entry);
                    parents.emplace(entry.th32ProcessID, entry.th32ParentProcessID);
                } while (Process32NextW(snapshot.get(), &entry));
            }
            for (const auto& process : processes) {
                if (_wcsicmp(process.szExeFile, L"language_server_windows_x64.exe") != 0 ||
                    !IsDescendantProcess(process.th32ProcessID, root_process_id, parents)) {
                    continue;
                }
                unsigned int& attempt_count = attempts[process.th32ProcessID];
                if (attempt_count >= 5) {
                    continue;
                }
                if (IsModuleLoaded(process.th32ProcessID, dll_path) ||
                    InjectRunningProcess(process.th32ProcessID, dll_path, options)) {
                    attempt_count = 5;
                } else {
                    ++attempt_count;
                }
            }
        }
        if (WaitForSingleObject(root_process.get(), 100) == WAIT_OBJECT_0) {
            break;
        }
    }
    return 0;
}

int WatchCursorProcess(DWORD root_process_id, const std::wstring& proxy, bool isolated) {
    std::wstring proxy_host;
    if (!easy_net::browser::ParseLiteralSocksEndpoint(proxy, proxy_host)) {
        return 2;
    }
    ScopedHandle root_process(OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION,
                                          FALSE, root_process_id));
    if (root_process.get() == nullptr) {
        return 4;
    }
    ScopedHandle ready(OpenEventW(EVENT_MODIFY_STATE, FALSE,
                                  CursorWatcherReadyEventName(root_process_id).c_str()));
    if (ready.get() == nullptr) {
        return 4;
    }
    Options options;
    options.proxy = proxy;
    options.cursor_isolated = isolated;
    ScopedHandle managed(CreateEventW(nullptr, TRUE, FALSE,
                                      CursorManagedEventName(options).c_str()));
    if (managed.get() == nullptr) {
        return 4;
    }
    ScopedHandle proxy_marker;
    if (!isolated) {
        proxy_marker = ScopedHandle(CreateEventW(nullptr, TRUE, FALSE,
                                                 CursorProxyEventName(proxy).c_str()));
        if (proxy_marker.get() == nullptr) {
            return 4;
        }
    }
    if (!SetEvent(ready.get())) {
        return 4;
    }
    WaitForSingleObject(root_process.get(), INFINITE);
    return 0;
}

int ActivatePackagedApplication(const Options& options,
                                const std::filesystem::path& dll_path) {
    const HRESULT com_result = CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);
    const bool uninitialize_com = SUCCEEDED(com_result);
    if (FAILED(com_result) && com_result != RPC_E_CHANGED_MODE) {
        std::wcerr << L"Cannot initialize COM for packaged app activation (HRESULT 0x"
                   << std::hex << static_cast<unsigned long>(com_result) << std::dec << L").\n";
        return 5;
    }

    IApplicationActivationManager* activation_manager = nullptr;
    HRESULT result = CoCreateInstance(CLSID_ApplicationActivationManager, nullptr,
                                      CLSCTX_LOCAL_SERVER,
                                      IID_PPV_ARGS(&activation_manager));
    DWORD process_id = 0;
    if (SUCCEEDED(result)) {
        result = activation_manager->ActivateApplication(options.app_user_model_id.c_str(),
                                                         nullptr, AO_NONE, &process_id);
        activation_manager->Release();
    }
    if (uninitialize_com) {
        CoUninitialize();
    }
    if (FAILED(result) || process_id == 0) {
        std::wcerr << L"Cannot activate packaged app " << options.app_user_model_id
                   << L" (HRESULT 0x" << std::hex << static_cast<unsigned long>(result)
                   << std::dec << L"). Use Get-StartApps to verify its AppID.\n";
        return 5;
    }

    std::wcout << L"Activated packaged app PID " << process_id << L" ("
               << options.app_user_model_id << L").\n";
    if (!InjectRunningProcess(process_id, dll_path, options)) {
        std::wcerr << L"The packaged app was activated normally, but its process could not be hooked.\n";
        return 5;
    }
    if (options.detach) {
        return 0;
    }

    ScopedHandle process(OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION,
                                     FALSE, process_id));
    if (process.get() == nullptr) {
        std::wcerr << L"Cannot wait for activated PID " << process_id << L" (error "
                   << GetLastError() << L").\n";
        return 5;
    }
    WaitForSingleObject(process.get(), INFINITE);
    DWORD exit_code = 1;
    if (!GetExitCodeProcess(process.get(), &exit_code)) {
        std::wcerr << L"Cannot read the target exit code (error " << GetLastError() << L").\n";
    }
    return static_cast<int>(exit_code);
}

}  // namespace

int wmain(int argc, wchar_t** argv) {
    if (argc == 2 && std::wstring_view(argv[1]) == L"--wechat-status") {
        const auto local_app_data = EnvironmentValue(L"LOCALAPPDATA");
        if (!local_app_data) {
            std::wcerr << L"LOCALAPPDATA is unavailable.\n";
            return 3;
        }
        const std::filesystem::path status_path =
            std::filesystem::path(*local_app_data) /
            L"EasyNetHook/WinDivert/wechat-status.json";
        const auto content = ReadUtf8File(status_path);
        if (!content) {
            std::wcerr << L"WinDivert supervisor status was not found: "
                       << status_path.wstring() << L"\n";
            return 7;
        }
        std::cout << *content;
        const bool fresh = easy_net::wechat::IsFreshStatus(*content, GetTickCount64());
        if (!fresh) {
            std::wcerr << L"WinDivert supervisor heartbeat is stale.\n";
        }
        return easy_net::wechat::IsHealthyStatus(*content) && fresh ? 0 : 7;
    }
    if ((argc == 4 || argc == 6 || argc == 11) &&
        std::wstring_view(argv[1]) == L"--wechat-watch") {
        wchar_t* root_end = nullptr;
        wchar_t* engine_end = nullptr;
        const unsigned long root_id = std::wcstoul(argv[2], &root_end, 10);
        const unsigned long engine_id = std::wcstoul(argv[3], &engine_end, 10);
        if (root_id == 0 || engine_id == 0 || root_end == nullptr || engine_end == nullptr ||
            *root_end != L'\0' || *engine_end != L'\0') {
            return 2;
        }
        if (argc == 4) {
            return WatchWeChat(static_cast<DWORD>(root_id), static_cast<DWORD>(engine_id));
        }
        wchar_t* handle_end = nullptr;
        const unsigned long long handle_value = std::wcstoull(argv[4], &handle_end, 10);
        if (handle_value == 0 || handle_end == nullptr || *handle_end != L'\0') {
            return 2;
        }
        if (argc == 11) {
            if (std::wstring_view(argv[6]) != L"windivert" ||
                (std::wstring_view(argv[10]) != L"0" &&
                 std::wstring_view(argv[10]) != L"1")) {
                return 2;
            }
            const auto proxy_text = ToUtf8(argv[9]);
            WeChatSupervisorConfig supervisor;
            supervisor.restart_windivert = true;
            supervisor.debug_log = std::wstring_view(argv[10]) == L"1";
            supervisor.engine_path = argv[7];
            supervisor.config_path = argv[8];
            supervisor.status_path = std::filesystem::path(argv[5]).parent_path() /
                                     L"wechat-status.json";
            supervisor.proxy_text = argv[9];
            if (!proxy_text || !easy_net::tun::ParseEndpoint(*proxy_text, supervisor.proxy) ||
                !std::filesystem::is_regular_file(supervisor.engine_path) ||
                !std::filesystem::is_regular_file(supervisor.config_path)) {
                return 2;
            }
            return WatchWeChat(
                static_cast<DWORD>(root_id), static_cast<DWORD>(engine_id),
                reinterpret_cast<HANDLE>(static_cast<std::uintptr_t>(handle_value)),
                std::filesystem::path(argv[5]), supervisor);
        }
        return WatchWeChat(static_cast<DWORD>(root_id), static_cast<DWORD>(engine_id),
                           reinterpret_cast<HANDLE>(static_cast<std::uintptr_t>(handle_value)),
                           std::filesystem::path(argv[5]));
    }
    if (argc >= 5 && std::wstring_view(argv[1]) == L"--antigravity-watch") {
        wchar_t* end = nullptr;
        const unsigned long process_id = std::wcstoul(argv[2], &end, 10);
        if (process_id == 0 || end == argv[2] || end == nullptr || *end != L'\0') {
            return 2;
        }
        std::wstring proxy;
        std::wstring dns;
        for (int index = 3; index < argc; ++index) {
            const std::wstring_view argument(argv[index]);
            if ((argument == L"--proxy" || argument == L"--dns") && index + 1 < argc) {
                if (argument == L"--proxy") {
                    proxy = argv[++index];
                } else {
                    dns = argv[++index];
                }
            } else {
                return 2;
            }
        }
        if (proxy.empty()) {
            return 2;
        }
        return WatchAntigravityLanguageServer(static_cast<DWORD>(process_id), proxy, dns);
    }
    if ((argc == 5 || argc == 6) && std::wstring_view(argv[1]) == L"--cursor-watch") {
        wchar_t* end = nullptr;
        const unsigned long process_id = std::wcstoul(argv[2], &end, 10);
        if (process_id == 0 || end == argv[2] || end == nullptr || *end != L'\0' ||
            std::wstring_view(argv[3]) != L"--proxy") {
            return 2;
        }
        const bool isolated = argc == 6 && std::wstring_view(argv[5]) == L"--isolated";
        if (argc == 6 && !isolated) {
            return 2;
        }
        return WatchCursorProcess(static_cast<DWORD>(process_id), argv[4], isolated);
    }
    if (argc == 1 || (argc == 2 && std::wstring_view(argv[1]) == L"--gui")) {
        std::vector<wchar_t> module_path(32768);
        const DWORD length = GetModuleFileNameW(nullptr, module_path.data(),
                                                static_cast<DWORD>(module_path.size()));
        if (length == 0 || length >= static_cast<DWORD>(module_path.size())) {
            MessageBoxW(nullptr, L"Cannot locate easy-net-hook.exe.", L"Easy-Net Hook",
                        MB_OK | MB_ICONERROR);
            return 3;
        }
        FreeConsole();
        return RunLauncherGui(GetModuleHandleW(nullptr),
                              std::wstring(module_path.data(), length));
    }
    Options options;
    if (!ParseOptions(argc, argv, options)) {
        PrintUsage();
        return 2;
    }
    if (options.chatgpt_web) {
        return LaunchChatGptWeb(options);
    }
    if (options.chatgpt_app) {
        return LaunchChatGptApp(options);
    }
    if (options.antigravity) {
        return LaunchAntigravity(options);
    }
    if (options.cursor) {
        return LaunchCursor(options);
    }
    if (options.wechat) {
        return LaunchWeChat(options);
    }
    if (!options.dns.empty()) {
        easy_net::dns::Endpoint dns_server;
        if (!easy_net::dns::ParseEndpoint(options.dns, dns_server)) {
            std::wcerr << L"Invalid --dns value. Use a literal address such as 223.5.5.5:53 "
                          L"or [2001:4860:4860::8888]:53.\n";
            return 2;
        }
    }

    const auto module_directory = CurrentModuleDirectory();
    if (!module_directory) {
        std::wcerr << L"Cannot locate the launcher directory (error " << GetLastError() << L").\n";
        return 3;
    }
    const std::filesystem::path dll_path = std::filesystem::path(*module_directory) / L"easy-net-hook.dll";
    if (!std::filesystem::is_regular_file(dll_path)) {
        std::wcerr << L"Hook DLL not found: " << dll_path.wstring() << L"\n";
        return 3;
    }
    if (options.process_id) {
        return InjectRunningProcess(*options.process_id, dll_path, options) ? 0 : 5;
    }
    if (!options.app_user_model_id.empty()) {
        return ActivatePackagedApplication(options, dll_path);
    }

    const auto detours_dll_path = ToDetoursPath(dll_path.wstring());
    if (!detours_dll_path) {
        std::wcerr << L"The DLL path cannot be represented by the current Windows ANSI code page. "
                      L"Move the package to an ASCII-only path.\n";
        return 3;
    }
    if (!SetConfigEnvironment(options)) {
        std::wcerr << L"Cannot prepare the child environment (error " << GetLastError() << L").\n";
        return 4;
    }

    std::wstring command_line = BuildCommandLine(options.command);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');

    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    PROCESS_INFORMATION process{};
    const DWORD flags = CREATE_DEFAULT_ERROR_MODE | CREATE_SUSPENDED;

    if (!DetourCreateProcessWithDllExW(
            nullptr,
            mutable_command.data(),
            nullptr,
            nullptr,
            options.gui_worker ? FALSE : TRUE,
            flags,
            nullptr,
            nullptr,
            &startup,
            &process,
            detours_dll_path->c_str(),
            nullptr)) {
        const DWORD error = GetLastError();
        std::wcerr << L"Cannot launch the target with the hook (error " << error << L").\n";
        if (error == ERROR_INVALID_HANDLE) {
            std::wcerr << L"The target and launcher probably have different architectures. "
                          L"Use the x86 package for a 32-bit target and x64 for a 64-bit target.\n";
        }
        return 5;
    }

    if (ResumeThread(process.hThread) == static_cast<DWORD>(-1)) {
        std::wcerr << L"Cannot resume the target process (error " << GetLastError() << L").\n";
        TerminateProcess(process.hProcess, 5);
        CloseHandle(process.hThread);
        CloseHandle(process.hProcess);
        return 5;
    }
    CloseHandle(process.hThread);

    std::wcout << L"Started PID " << process.dwProcessId << L" through SOCKS5 " << options.proxy << L".\n";
    if (options.detach) {
        CloseHandle(process.hProcess);
        return 0;
    }

    WaitForSingleObject(process.hProcess, INFINITE);
    DWORD exit_code = 1;
    if (!GetExitCodeProcess(process.hProcess, &exit_code)) {
        std::wcerr << L"Cannot read the target exit code (error " << GetLastError() << L").\n";
    }
    CloseHandle(process.hProcess);
    return static_cast<int>(exit_code);
}
