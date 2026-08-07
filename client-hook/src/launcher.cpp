#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <detours.h>

#include <filesystem>
#include <iostream>
#include <optional>
#include <string>
#include <vector>

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
    std::vector<std::wstring> command;
};

void PrintUsage() {
    std::wcerr
        << L"Easy-Net Hook - launch one Windows application through a SOCKS5 proxy\n\n"
        << L"Usage:\n"
        << L"  easy-net-hook.exe --proxy 127.0.0.1:1080 [--dns DNS] [options] -- app.exe [args...]\n\n"
        << L"Options:\n"
        << L"  --username VALUE       SOCKS5 username (optional)\n"
        << L"  --password VALUE       SOCKS5 password (optional, max 255 bytes)\n"
        << L"  --dns IP[:PORT]        Use a specific DNS server (default: Windows DNS)\n"
        << L"  --no-children          Do not inject the hook into child processes\n"
        << L"  --allow-udp-direct     Allow UDP to bypass the proxy (may leak traffic)\n"
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
                   argument == L"--password" || argument == L"--dns") {
            if (++index >= argc) {
                std::wcerr << L"Missing value for " << argument << L".\n";
                return false;
            }
            if (argument == L"--proxy") {
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
    if (options.command.empty()) {
        std::wcerr << L"A target command is required after --.\n";
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

}  // namespace

int wmain(int argc, wchar_t** argv) {
    Options options;
    if (!ParseOptions(argc, argv, options)) {
        PrintUsage();
        return 2;
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
