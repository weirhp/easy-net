#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <detours.h>
#include <shobjidl.h>
#include <tlhelp32.h>

#include <cstdint>
#include <cstring>
#include <cwchar>
#include <filesystem>
#include <iomanip>
#include <iostream>
#include <iterator>
#include <optional>
#include <string>
#include <system_error>
#include <vector>

#include "browser_proxy.h"
#include "config_ipc.h"
#include "dns_resolver.h"

namespace {

struct Options {
    std::wstring proxy;
    std::wstring username;
    std::wstring password;
    std::wstring dns;
    bool inject_children = true;
    bool allow_udp_direct = false;
    bool detach = false;
    bool chatgpt_web = false;
    std::wstring browser_path;
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
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 --chatgpt-web [options]\n\n"
        << L"Options:\n"
        << L"  --username VALUE       SOCKS5 username (optional)\n"
        << L"  --password VALUE       SOCKS5 password (optional, max 255 bytes)\n"
        << L"  --dns IP[:PORT]        Use a specific DNS server (default: Windows DNS)\n"
        << L"  --no-children          Do not inject the hook into child processes\n"
        << L"  --allow-udp-direct     Allow UDP to bypass the proxy (may leak traffic)\n"
        << L"  --pid PID              Inject into an already running process\n"
        << L"  --appx AUMID           Activate a packaged desktop app, then inject it\n"
        << L"  --chatgpt-web          Open ChatGPT in an isolated Edge/Chrome SOCKS5 session\n"
        << L"  --browser-path PATH    Browser executable for --chatgpt-web (optional)\n"
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
        } else if (argument == L"--chatgpt-web") {
            options.chatgpt_web = true;
        } else if (argument == L"--detach") {
            options.detach = true;
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
    const int target_count = (!options.command.empty() ? 1 : 0) +
                             (options.process_id.has_value() ? 1 : 0) +
                             (!options.app_user_model_id.empty() ? 1 : 0) +
                             (options.chatgpt_web ? 1 : 0);
    if (target_count != 1) {
        std::wcerr << L"Specify exactly one target: a command after --, --pid PID, "
                      L"--appx AUMID, or --chatgpt-web.\n";
        return false;
    }
    if (!options.browser_path.empty() && !options.chatgpt_web) {
        std::wcerr << L"--browser-path can only be used with --chatgpt-web.\n";
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
        L"--proxy-server=socks5://" + options.proxy,
        L"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE " + proxy_host,
        L"--disable-quic",
        L"--disable-background-networking",
        L"--disable-component-update",
        L"--no-first-run",
        L"--no-default-browser-check",
        L"https://chatgpt.com/",
    };
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
    Options options;
    if (!ParseOptions(argc, argv, options)) {
        PrintUsage();
        return 2;
    }
    if (options.chatgpt_web) {
        return LaunchChatGptWeb(options);
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
            TRUE,
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
