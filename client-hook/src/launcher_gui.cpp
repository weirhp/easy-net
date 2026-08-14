#include "launcher_gui.h"

#include <commctrl.h>
#include <commdlg.h>
#include <shellapi.h>
#include <shlobj.h>

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <cwchar>
#include <filesystem>
#include <iterator>
#include <limits>
#include <string>
#include <system_error>
#include <utility>
#include <unordered_map>
#include <vector>

#include "history_store.h"
#include "dns_resolver.h"
#include "resource.h"
#include "socks5_health.h"
#include "tun_config.h"

namespace {

constexpr wchar_t kModeChatGpt[] = L"chatgpt";
constexpr wchar_t kModeAntigravity[] = L"antigravity";
constexpr wchar_t kModeCursor[] = L"cursor";
constexpr wchar_t kModeWeChat[] = L"wechat";
constexpr wchar_t kModeWeChatWinDivert[] = L"wechat-windivert";
constexpr wchar_t kModeHook[] = L"hook";
constexpr UINT_PTR kLaunchTimer = 1;
constexpr std::size_t kMaximumLaunchOutput = 32 * 1024;

bool IsWeChatMode(const std::wstring& mode) {
    return mode == kModeWeChat || mode == kModeWeChatWinDivert;
}

struct GuiState {
    HINSTANCE instance = nullptr;
    HWND dialog = nullptr;
    std::wstring launcher_path;
    std::filesystem::path entries_path;
    std::vector<easy_net::history::Entry> entries;
    std::size_t editing_index = std::numeric_limits<std::size_t>::max();
    HIMAGELIST entry_images = nullptr;
    std::unordered_map<std::wstring, HICON> icon_cache;
    HFONT title_font = nullptr;
    bool updating = false;
    bool dirty = false;
    bool launching = false;
    HANDLE launch_process = nullptr;
    HANDLE launch_output = nullptr;
    std::string launch_output_bytes;
    easy_net::history::Entry pending_entry;
    ULONGLONG launch_started_tick = 0;
};

GuiState* State(HWND dialog) {
    return reinterpret_cast<GuiState*>(GetWindowLongPtrW(dialog, DWLP_USER));
}

std::wstring ControlText(HWND dialog, int identifier) {
    const HWND control = GetDlgItem(dialog, identifier);
    const int length = GetWindowTextLengthW(control);
    std::wstring value(static_cast<std::size_t>(length) + 1, L'\0');
    GetWindowTextW(control, value.data(), length + 1);
    value.resize(static_cast<std::size_t>(length));
    return value;
}

void SetControlText(HWND dialog, int identifier, const std::wstring& value) {
    SetWindowTextW(GetDlgItem(dialog, identifier), value.c_str());
}

std::string ToUtf8(const std::wstring& value) {
    if (value.empty()) {
        return {};
    }
    const int required = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
                                             static_cast<int>(value.size()), nullptr, 0,
                                             nullptr, nullptr);
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

std::wstring DecodeProcessOutput(const std::string& value) {
    if (value.empty()) {
        return {};
    }
    UINT code_page = CP_UTF8;
    DWORD flags = MB_ERR_INVALID_CHARS;
    int required = MultiByteToWideChar(code_page, flags, value.data(),
                                       static_cast<int>(value.size()), nullptr, 0);
    if (required <= 0) {
        code_page = CP_ACP;
        flags = 0;
        required = MultiByteToWideChar(code_page, flags, value.data(),
                                       static_cast<int>(value.size()), nullptr, 0);
    }
    if (required <= 0) {
        return {};
    }
    std::wstring result(static_cast<std::size_t>(required), L'\0');
    MultiByteToWideChar(code_page, flags, value.data(), static_cast<int>(value.size()),
                        result.data(), required);
    while (!result.empty() && (result.back() == L'\r' || result.back() == L'\n' ||
                               result.back() == L' ' || result.back() == L'\t')) {
        result.pop_back();
    }
    return result;
}

std::wstring QuoteArgument(const std::wstring& argument) {
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

std::filesystem::path StoragePath(const wchar_t* filename) {
    const DWORD required = GetEnvironmentVariableW(L"LOCALAPPDATA", nullptr, 0);
    if (required == 0) {
        return {};
    }
    std::wstring local_app_data(required, L'\0');
    const DWORD written =
        GetEnvironmentVariableW(L"LOCALAPPDATA", local_app_data.data(), required);
    if (written == 0 || written >= required) {
        return {};
    }
    local_app_data.resize(written);
    return std::filesystem::path(local_app_data) / L"EasyNetHook" / filename;
}

std::vector<easy_net::history::Entry> LoadHistory(const std::filesystem::path& path) {
    if (path.empty()) {
        return {};
    }
    HANDLE file = CreateFileW(path.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr, OPEN_EXISTING,
                              FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) {
        return {};
    }
    const DWORD byte_count = GetFileSize(file, nullptr);
    if (byte_count == INVALID_FILE_SIZE || byte_count < sizeof(wchar_t) ||
        byte_count > 1024 * 1024 || byte_count % sizeof(wchar_t) != 0) {
        CloseHandle(file);
        return {};
    }
    std::vector<unsigned char> bytes(byte_count);
    DWORD bytes_read = 0;
    const BOOL read = ReadFile(file, bytes.data(), byte_count, &bytes_read, nullptr);
    CloseHandle(file);
    if (!read || bytes_read != byte_count) {
        return {};
    }
    std::size_t offset = 0;
    if (bytes[0] == 0xff && bytes[1] == 0xfe) {
        offset = sizeof(wchar_t);
    }
    const std::size_t character_count = (bytes.size() - offset) / sizeof(wchar_t);
    std::wstring text(character_count, L'\0');
    if (!text.empty()) {
        memcpy(text.data(), bytes.data() + offset, text.size() * sizeof(wchar_t));
    }
    return easy_net::history::Parse(text);
}

bool SaveEntries(const GuiState& state) {
    if (state.entries_path.empty()) {
        return false;
    }
    std::error_code error;
    std::filesystem::create_directories(state.entries_path.parent_path(), error);
    if (error) {
        return false;
    }
    HANDLE file = CreateFileW(state.entries_path.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_ALWAYS,
                              FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) {
        return false;
    }
    const std::wstring content = easy_net::history::Serialize(state.entries);
    constexpr wchar_t byte_order_mark = 0xfeff;
    DWORD written = 0;
    bool succeeded =
        WriteFile(file, &byte_order_mark, sizeof(byte_order_mark), &written, nullptr) &&
        written == sizeof(byte_order_mark);
    if (succeeded && !content.empty()) {
        const DWORD byte_count = static_cast<DWORD>(content.size() * sizeof(wchar_t));
        written = 0;
        succeeded = WriteFile(file, content.data(), byte_count, &written, nullptr) &&
                    written == byte_count;
    }
    CloseHandle(file);
    return succeeded;
}

std::wstring CurrentTimestamp() {
    SYSTEMTIME time{};
    GetLocalTime(&time);
    wchar_t value[32]{};
    swprintf_s(value, L"%04u-%02u-%02u %02u:%02u", time.wYear, time.wMonth, time.wDay,
               time.wHour, time.wMinute);
    return value;
}

std::wstring NewEntryId() {
    GUID guid{};
    if (FAILED(CoCreateGuid(&guid))) {
        return std::to_wstring(GetCurrentProcessId()) + L"-" +
               std::to_wstring(GetTickCount64());
    }
    wchar_t value[40]{};
    if (StringFromGUID2(guid, value, static_cast<int>(std::size(value))) == 0) {
        return {};
    }
    std::wstring result(value);
    result.erase(std::remove_if(result.begin(), result.end(), [](wchar_t character) {
                     return character == L'{' || character == L'}' || character == L'-';
                 }),
                 result.end());
    return result;
}

bool EnsureEntryIds(std::vector<easy_net::history::Entry>& entries) {
    bool changed = false;
    for (auto& entry : entries) {
        if (entry.id.empty()) {
            entry.id = NewEntryId();
            changed = true;
        }
    }
    return changed;
}

int ModeIndex(const std::wstring& mode) {
    if (mode == kModeAntigravity) {
        return 1;
    }
    if (mode == kModeHook) {
        return 5;
    }
    if (mode == kModeWeChat) {
        return 3;
    }
    if (mode == kModeWeChatWinDivert) {
        return 4;
    }
    if (mode == kModeCursor) {
        return 2;
    }
    return 0;
}

const wchar_t* ModeValue(int index) {
    if (index == 1) {
        return kModeAntigravity;
    }
    if (index == 2) {
        return kModeCursor;
    }
    if (index == 3) {
        return kModeWeChat;
    }
    if (index == 4) {
        return kModeWeChatWinDivert;
    }
    if (index == 5) {
        return kModeHook;
    }
    return kModeChatGpt;
}

const wchar_t* ModeLabel(const std::wstring& mode) {
    if (mode == kModeAntigravity) {
        return L"Antigravity";
    }
    if (mode == kModeHook) {
        return L"通用 Hook";
    }
    if (mode == kModeCursor) {
        return L"Cursor";
    }
    if (mode == kModeWeChat) {
        return L"微信 TUN";
    }
    if (mode == kModeWeChatWinDivert) {
        return L"微信 WinDivert";
    }
    return L"ChatGPT";
}

int ScaleForDpi(HWND window, int value) {
    const UINT dpi = GetDpiForWindow(window);
    return MulDiv(value, static_cast<int>(dpi == 0 ? USER_DEFAULT_SCREEN_DPI : dpi),
                  USER_DEFAULT_SCREEN_DPI);
}

void ClearIconCache(GuiState& state) {
    for (const auto& [path, icon] : state.icon_cache) {
        static_cast<void>(path);
        if (icon != nullptr) DestroyIcon(icon);
    }
    state.icon_cache.clear();
}

HICON EntryIcon(GuiState& state, const easy_net::history::Entry& entry) {
    if (!entry.path.empty()) {
        const auto cached = state.icon_cache.find(entry.path);
        if (cached != state.icon_cache.end()) {
            return cached->second;
        }
        HICON icon = nullptr;
        const int icon_size = ScaleForDpi(state.dialog, 28);
        if (PrivateExtractIconsW(entry.path.c_str(), 0, icon_size, icon_size, &icon, nullptr, 1,
                                 0) == 1 && icon != nullptr) {
            state.icon_cache.emplace(entry.path, icon);
            return icon;
        }
    }
    LPCWSTR resource = IDI_APPLICATION;
    if (entry.mode == kModeWeChat || entry.mode == kModeWeChatWinDivert) {
        resource = IDI_SHIELD;
    } else if (entry.mode == kModeChatGpt) {
        resource = IDI_INFORMATION;
    }
    return static_cast<HICON>(LoadImageW(nullptr, resource, IMAGE_ICON,
                                        ScaleForDpi(state.dialog, 28),
                                        ScaleForDpi(state.dialog, 28), LR_SHARED));
}

void RefreshEntries(GuiState& state, std::size_t selected_index =
                                              std::numeric_limits<std::size_t>::max()) {
    const bool previous_updating = state.updating;
    state.updating = true;
    const HWND list = GetDlgItem(state.dialog, IDC_ENTRIES);
    ListView_DeleteAllItems(list);
    if (state.entry_images != nullptr) {
        ImageList_RemoveAll(state.entry_images);
    }
    for (std::size_t index = 0; index < state.entries.size(); ++index) {
        const auto& entry = state.entries[index];
        HICON icon = EntryIcon(state, entry);
        const int image_index = state.entry_images != nullptr && icon != nullptr
                                    ? ImageList_AddIcon(state.entry_images, icon)
                                    : -1;
        LVITEMW item{};
        item.mask = LVIF_TEXT | LVIF_PARAM | LVIF_IMAGE;
        item.iItem = static_cast<int>(index);
        item.pszText = const_cast<wchar_t*>(entry.name.c_str());
        item.lParam = static_cast<LPARAM>(index);
        item.iImage = image_index;
        const int row = ListView_InsertItem(list, &item);
        ListView_SetItemText(list, row, 1,
                             const_cast<wchar_t*>(ModeLabel(entry.mode)));
        ListView_SetItemText(list, row, 2, const_cast<wchar_t*>(entry.proxy.c_str()));
        UINT columns[]{1, 2};
        LVTILEINFO tile{};
        tile.cbSize = sizeof(tile);
        tile.iItem = row;
        tile.cColumns = static_cast<UINT>(std::size(columns));
        tile.puColumns = columns;
        ListView_SetTileInfo(list, &tile);
    }
    if (selected_index < state.entries.size()) {
        ListView_SetItemState(list, static_cast<int>(selected_index),
                              LVIS_SELECTED | LVIS_FOCUSED,
                              LVIS_SELECTED | LVIS_FOCUSED);
        ListView_EnsureVisible(list, static_cast<int>(selected_index), FALSE);
    }
    state.updating = previous_updating;
}

void UpdateModeUi(GuiState& state) {
    const int mode = static_cast<int>(SendDlgItemMessageW(state.dialog, IDC_MODE, CB_GETCURSEL,
                                                           0, 0));
    const bool chatgpt = mode == 0;
    const bool antigravity = mode == 1;
    const bool cursor = mode == 2;
    const bool wechat = mode == 3 || mode == 4;
    const bool existing_wechat = wechat &&
        IsDlgButtonChecked(state.dialog, IDC_WECHAT_EXISTING) == BST_CHECKED;
    EnableWindow(GetDlgItem(state.dialog, IDC_PATH), !chatgpt && !existing_wechat);
    EnableWindow(GetDlgItem(state.dialog, IDC_BROWSE), !chatgpt && !existing_wechat);
    EnableWindow(GetDlgItem(state.dialog, IDC_ARGUMENTS), !chatgpt && !existing_wechat);
    EnableWindow(GetDlgItem(state.dialog, IDC_DNS), !chatgpt && !cursor && mode != 4);
    ShowWindow(GetDlgItem(state.dialog, IDC_ISOLATED),
               antigravity || cursor ? SW_SHOW : SW_HIDE);
    EnableWindow(GetDlgItem(state.dialog, IDC_ISOLATED), antigravity || cursor);
    ShowWindow(GetDlgItem(state.dialog, IDC_WECHAT_EXISTING), wechat ? SW_SHOW : SW_HIDE);
    EnableWindow(GetDlgItem(state.dialog, IDC_WECHAT_EXISTING), wechat);
    ShowWindow(GetDlgItem(state.dialog, IDC_UDP_LABEL), wechat ? SW_SHOW : SW_HIDE);
    ShowWindow(GetDlgItem(state.dialog, IDC_UDP_MODE), wechat ? SW_SHOW : SW_HIDE);
    EnableWindow(GetDlgItem(state.dialog, IDC_UDP_MODE), wechat);
    EnableWindow(GetDlgItem(state.dialog, IDC_WECHAT_STATUS), mode == 4);
    if (chatgpt) {
        SetControlText(state.dialog, IDC_PATH, L"");
        SetControlText(state.dialog, IDC_HINT,
                       L"ChatGPT 使用独立配置，界面与 codex.exe 后端都通过代理。首次需登录。\r\n"
                       L"启动成功后可关闭 Easy-Net Hook，不影响 ChatGPT 使用。");
    } else if (mode == 1) {
        SetControlText(state.dialog, IDC_HINT,
                       L"默认复用桌面版登录状态；启动前请完全退出 Antigravity。独立配置需单独登录。\r\n"
                       L"启动后可关闭本窗口，language server 监视器会在后台继续运行。");
    } else if (mode == 2) {
        SetControlText(state.dialog, IDC_HINT,
                       L"Chromium 原生代理 + AI/扩展 Node 服务 Hook，Opus 无需全局 TUN。\r\n"
                       L"首次需完全退出直连 Cursor；相同代理可继续开新窗口。");
    } else if (mode == 3) {
        SetControlText(state.dialog, IDC_HINT,
                       existing_wechat
                           ? L"将 TUN 接管到已运行微信；只有新连接生效。需要管理员权限。\r\n关闭本窗口不影响代理，微信退出后 TUN 自动停止。"
                           : L"通过 TUN 启动微信并代理 TCP/UDP；需要管理员权限，资源占用较高。\r\n程序路径可留空自动查找；启动前请完全退出微信。");
    } else if (mode == 4) {
        SetControlText(state.dialog, IDC_HINT,
                       existing_wechat
                           ? L"推荐：接管已运行微信的新连接，WinDivert 守护器会自动重启并检测代理。\r\n关闭本窗口不影响代理；微信退出后守护器自动停止。"
                           : L"推荐：通过 WinDivert 启动微信，代理 TCP/UDP，通常比 TUN 更省资源。\r\n需要管理员权限；DNS 保持 Windows 系统设置。");
    } else {
        SetControlText(state.dialog, IDC_HINT,
                       L"适合普通 Win32 程序：DLL Hook 代理 TCP，外部 UDP 默认阻断。\r\n"
                       L"启动后可关闭本窗口；受保护进程或特殊网络栈可能不兼容。");
    }
}

void TestProxy(GuiState& state) {
    const std::string proxy = ToUtf8(ControlText(state.dialog, IDC_PROXY));
    easy_net::tun::Endpoint endpoint;
    if (proxy.empty() || !easy_net::tun::ParseEndpoint(proxy, endpoint)) {
        MessageBoxW(state.dialog, L"请输入有效的 SOCKS5 地址，例如 127.0.0.1:1082。",
                    L"代理地址无效", MB_OK | MB_ICONWARNING);
        return;
    }
    SetControlText(state.dialog, IDC_STATUS, L"正在测试 SOCKS5 握手…");
    const bool responsive = easy_net::socks5_health::Responsive(endpoint);
    SetControlText(state.dialog, IDC_STATUS,
                   responsive ? L"代理可连接，并返回了 SOCKS5 握手响应"
                              : L"代理不可连接或未返回有效 SOCKS5 响应");
    MessageBoxW(state.dialog,
                responsive
                    ? L"SOCKS5 服务可连接并返回了有效握手响应。\r\n"
                      L"此测试不校验账号密码，也不代表所有目标网站都可访问。"
                    : L"无法连接该地址，或服务没有返回有效 SOCKS5 握手。\r\n"
                      L"请确认 Easy-Net Lite 已运行、端口正确且防火墙未阻止。",
                responsive ? L"代理测试成功" : L"代理测试失败",
                MB_OK | (responsive ? MB_ICONINFORMATION : MB_ICONERROR));
}

void OpenLogs(GuiState& state) {
    const auto path = StoragePath(L"logs").parent_path();
    if (path.empty()) {
        MessageBoxW(state.dialog, L"LOCALAPPDATA 不可用，无法确定日志目录。", L"打开失败",
                    MB_OK | MB_ICONERROR);
        return;
    }
    std::error_code error;
    std::filesystem::create_directories(path, error);
    const HINSTANCE result = ShellExecuteW(state.dialog, L"open", path.c_str(), nullptr, nullptr,
                                           SW_SHOWNORMAL);
    if (reinterpret_cast<INT_PTR>(result) <= 32) {
        MessageBoxW(state.dialog, L"无法打开 Easy-Net Hook 日志目录。", L"打开失败",
                    MB_OK | MB_ICONERROR);
    }
}

void ShowWeChatStatus(GuiState& state) {
    const auto path = StoragePath(L"WinDivert/wechat-status.json");
    HANDLE file = CreateFileW(path.c_str(), GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE,
                              nullptr, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) {
        MessageBoxW(state.dialog, L"没有找到微信 WinDivert 运行状态。守护器可能尚未启动。",
                    L"微信代理状态", MB_OK | MB_ICONWARNING);
        return;
    }
    const DWORD size = GetFileSize(file, nullptr);
    std::string content(size != INVALID_FILE_SIZE && size < 64 * 1024 ? size : 0, '\0');
    DWORD read = 0;
    const bool succeeded = !content.empty() &&
                           ReadFile(file, content.data(), size, &read, nullptr) && read == size;
    CloseHandle(file);
    if (!succeeded) {
        MessageBoxW(state.dialog, L"微信状态文件无法读取或内容为空。", L"微信代理状态",
                    MB_OK | MB_ICONWARNING);
        return;
    }
    const std::wstring status = DecodeProcessOutput(content);
    MessageBoxW(state.dialog, status.c_str(), L"微信 WinDivert 运行状态",
                MB_OK | MB_ICONINFORMATION);
}

void LoadEntry(GuiState& state, std::size_t index) {
    if (index >= state.entries.size()) {
        return;
    }
    state.updating = true;
    state.editing_index = index;
    const auto& entry = state.entries[index];
    SendDlgItemMessageW(state.dialog, IDC_MODE, CB_SETCURSEL, ModeIndex(entry.mode), 0);
    SetControlText(state.dialog, IDC_ENTRY_NAME, entry.name);
    SetControlText(state.dialog, IDC_PROXY, entry.proxy);
    SetControlText(state.dialog, IDC_PATH, entry.path);
    SetControlText(state.dialog, IDC_ARGUMENTS, entry.arguments);
    SetControlText(state.dialog, IDC_DNS, entry.dns);
    CheckDlgButton(state.dialog, IDC_ISOLATED,
                   entry.isolated ? BST_CHECKED : BST_UNCHECKED);
    CheckDlgButton(state.dialog, IDC_WECHAT_EXISTING,
                   entry.wechat_existing ? BST_CHECKED : BST_UNCHECKED);
    int udp_index = 0;
    if (entry.udp_mode == L"proxy") udp_index = 1;
    else if (entry.udp_mode == L"block") udp_index = 2;
    else if (entry.udp_mode == L"direct") udp_index = 3;
    SendDlgItemMessageW(state.dialog, IDC_UDP_MODE, CB_SETCURSEL, udp_index, 0);
    UpdateModeUi(state);
    if (entry.mode != kModeChatGpt) {
        SetControlText(state.dialog, IDC_PATH, entry.path);
    }
    SetControlText(state.dialog, IDC_ENTRY_STATE, L"正在编辑：" + entry.name);
    SetControlText(state.dialog, IDC_STATUS, L"修改后可保存或直接启动");
    EnableWindow(GetDlgItem(state.dialog, IDC_REMOVE_ENTRY), TRUE);
    state.dirty = false;
    state.updating = false;
}

int SelectedEntryIndex(HWND dialog) {
    return ListView_GetNextItem(GetDlgItem(dialog, IDC_ENTRIES), -1, LVNI_SELECTED);
}

std::wstring DefaultEntryName(int mode) {
    if (mode == 1) {
        return L"Antigravity IDE";
    }
    if (mode == 2) {
        return L"Cursor";
    }
    if (mode == 3) {
        return L"微信 TUN";
    }
    if (mode == 4) {
        return L"微信 WinDivert";
    }
    if (mode == 5) {
        return L"我的程序";
    }
    return L"ChatGPT";
}

bool IsDefaultEntryName(const std::wstring& name) {
    for (int mode = 0; mode < 6; ++mode) {
        if (name == DefaultEntryName(mode)) {
            return true;
        }
    }
    return name.empty();
}

void NewEntry(GuiState& state) {
    state.updating = true;
    state.editing_index = std::numeric_limits<std::size_t>::max();
    ListView_SetItemState(GetDlgItem(state.dialog, IDC_ENTRIES), -1, 0,
                          LVIS_SELECTED | LVIS_FOCUSED);
    SendDlgItemMessageW(state.dialog, IDC_MODE, CB_SETCURSEL, 0, 0);
    SendDlgItemMessageW(state.dialog, IDC_UDP_MODE, CB_SETCURSEL, 0, 0);
    SetControlText(state.dialog, IDC_ENTRY_NAME, DefaultEntryName(0));
    SetControlText(state.dialog, IDC_PROXY, L"127.0.0.1:1082");
    SetControlText(state.dialog, IDC_PATH, L"");
    SetControlText(state.dialog, IDC_ARGUMENTS, L"");
    SetControlText(state.dialog, IDC_DNS, L"");
    CheckDlgButton(state.dialog, IDC_ISOLATED, BST_UNCHECKED);
    CheckDlgButton(state.dialog, IDC_WECHAT_EXISTING, BST_UNCHECKED);
    EnableWindow(GetDlgItem(state.dialog, IDC_REMOVE_ENTRY), FALSE);
    SetControlText(state.dialog, IDC_ENTRY_STATE, L"新入口将在首次保存或启动后出现在左侧");
    SetControlText(state.dialog, IDC_STATUS, L"正在新建入口");
    UpdateModeUi(state);
    state.dirty = false;
    state.updating = false;
    SetFocus(GetDlgItem(state.dialog, IDC_ENTRY_NAME));
    SendDlgItemMessageW(state.dialog, IDC_ENTRY_NAME, EM_SETSEL, 0, -1);
}

bool BrowseForExecutable(GuiState& state) {
    std::vector<wchar_t> buffer(32768, L'\0');
    const std::wstring current = ControlText(state.dialog, IDC_PATH);
    if (!current.empty() && current.size() + 1 < buffer.size()) {
        wcscpy_s(buffer.data(), buffer.size(), current.c_str());
    }
    constexpr wchar_t filter[] = L"Windows 程序 (*.exe)\0*.exe\0所有文件 (*.*)\0*.*\0\0";
    OPENFILENAMEW dialog{};
    dialog.lStructSize = sizeof(dialog);
    dialog.hwndOwner = state.dialog;
    dialog.lpstrFilter = filter;
    dialog.lpstrFile = buffer.data();
    dialog.nMaxFile = static_cast<DWORD>(buffer.size());
    dialog.Flags = OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR;
    dialog.lpstrTitle = L"选择要通过 SOCKS5 启动的程序";
    if (!GetOpenFileNameW(&dialog)) {
        return false;
    }
    SetControlText(state.dialog, IDC_PATH, buffer.data());
    return true;
}

easy_net::history::Entry CurrentEntry(GuiState& state) {
    const int mode = static_cast<int>(SendDlgItemMessageW(state.dialog, IDC_MODE, CB_GETCURSEL,
                                                           0, 0));
    easy_net::history::Entry entry;
    if (state.editing_index < state.entries.size()) {
        entry.id = state.entries[state.editing_index].id;
    }
    if (entry.id.empty()) {
        entry.id = NewEntryId();
    }
    entry.mode = ModeValue(mode);
    entry.name = ControlText(state.dialog, IDC_ENTRY_NAME);
    entry.path = ControlText(state.dialog, IDC_PATH);
    entry.arguments = ControlText(state.dialog, IDC_ARGUMENTS);
    entry.proxy = ControlText(state.dialog, IDC_PROXY);
    entry.dns = ControlText(state.dialog, IDC_DNS);
    if (mode == 2 || mode == 4) {
        entry.dns.clear();
    }
    entry.isolated = (mode == 1 || mode == 2) &&
                     IsDlgButtonChecked(state.dialog, IDC_ISOLATED) == BST_CHECKED;
    entry.wechat_existing = (mode == 3 || mode == 4) &&
                            IsDlgButtonChecked(state.dialog, IDC_WECHAT_EXISTING) ==
                                BST_CHECKED;
    if (entry.wechat_existing) {
        entry.path.clear();
        entry.arguments.clear();
    }
    if (mode == 3 || mode == 4) {
        const int udp_mode = static_cast<int>(SendDlgItemMessageW(
            state.dialog, IDC_UDP_MODE, CB_GETCURSEL, 0, 0));
        constexpr const wchar_t* values[]{L"auto", L"proxy", L"block", L"direct"};
        entry.udp_mode = udp_mode >= 0 && udp_mode < 4 ? values[udp_mode] : L"auto";
    }
    entry.last_used = CurrentTimestamp();
    if (entry.name.empty() && mode == 5 && !entry.path.empty()) {
        entry.name = std::filesystem::path(entry.path).stem().wstring();
    } else if (entry.name.empty()) {
        entry.name = DefaultEntryName(mode);
    }
    return entry;
}

bool ValidateEntry(HWND owner, const easy_net::history::Entry& entry) {
    if (entry.proxy.empty()) {
        MessageBoxW(owner, L"请输入 SOCKS5 代理地址，例如 127.0.0.1:1082。", L"缺少代理地址",
                    MB_OK | MB_ICONWARNING);
        return false;
    }
    const std::string proxy = ToUtf8(entry.proxy);
    easy_net::tun::Endpoint proxy_endpoint;
    if (proxy.empty() || !easy_net::tun::ParseEndpoint(proxy, proxy_endpoint)) {
        MessageBoxW(owner,
                    L"SOCKS5 地址格式无效。请输入 IPv4:端口，例如 127.0.0.1:1082；"
                    L"IPv6 请使用 [::1]:1082。",
                    L"代理地址无效", MB_OK | MB_ICONWARNING);
        return false;
    }
    if (!entry.dns.empty()) {
        easy_net::dns::Endpoint dns_endpoint;
        if (!easy_net::dns::ParseEndpoint(entry.dns, dns_endpoint)) {
            MessageBoxW(owner,
                        L"DNS 地址格式无效。可输入 223.5.5.5 或 223.5.5.5:53；"
                        L"留空则使用 Windows DNS。",
                        L"DNS 地址无效", MB_OK | MB_ICONWARNING);
            return false;
        }
    }
    if (entry.mode == kModeHook && entry.path.empty()) {
        MessageBoxW(owner, L"通用 Hook 模式需要选择一个程序。", L"缺少程序路径",
                    MB_OK | MB_ICONWARNING);
        return false;
    }
    std::error_code path_error;
    if (!entry.path.empty() &&
        !std::filesystem::is_regular_file(std::filesystem::path(entry.path), path_error)) {
        MessageBoxW(owner, L"选择的程序不存在或不可访问。", L"程序路径无效",
                    MB_OK | MB_ICONWARNING);
        return false;
    }
    return true;
}

std::wstring BuildLauncherCommand(const std::wstring& launcher_path,
                                  const easy_net::history::Entry& entry,
                                  bool gui_worker) {
    std::wstring command = QuoteArgument(launcher_path) + L" --proxy " +
                           QuoteArgument(entry.proxy) + L" --detach";
    if (gui_worker) {
        command += L" --gui-worker";
    }
    if (entry.mode == kModeChatGpt) {
        command += L" --chatgpt-app";
    } else if (entry.mode == kModeAntigravity) {
        command += L" --antigravity";
        if (entry.isolated) {
            command += L" --antigravity-isolated";
        }
        if (!entry.path.empty()) {
            command += L" --antigravity-path " + QuoteArgument(entry.path);
        }
        if (!entry.dns.empty()) {
            command += L" --dns " + QuoteArgument(entry.dns);
        }
        if (!entry.arguments.empty()) {
            command += L" -- " + entry.arguments;
        }
    } else if (entry.mode == kModeCursor) {
        command += L" --cursor";
        if (entry.isolated) {
            command += L" --cursor-isolated";
        }
        if (!entry.path.empty()) {
            command += L" --cursor-path " + QuoteArgument(entry.path);
        }
        if (!entry.dns.empty()) {
            command += L" --dns " + QuoteArgument(entry.dns);
        }
        if (!entry.arguments.empty()) {
            command += L" -- " + entry.arguments;
        }
    } else if (IsWeChatMode(entry.mode)) {
        command += entry.wechat_existing ? L" --wechat-existing" : L" --wechat";
        command += L" --tun-udp " +
                   QuoteArgument(entry.udp_mode.empty() ? L"auto" : entry.udp_mode);
        if (entry.mode == kModeWeChatWinDivert) {
            command += L" --wechat-backend windivert";
        }
        if (!entry.wechat_existing && !entry.path.empty()) {
            command += L" --wechat-path " + QuoteArgument(entry.path);
        }
        if (!entry.dns.empty()) {
            command += L" --dns " + QuoteArgument(entry.dns);
        }
        if (!entry.wechat_existing && !entry.arguments.empty()) {
            command += L" -- " + entry.arguments;
        }
    } else {
        if (!entry.dns.empty()) {
            command += L" --dns " + QuoteArgument(entry.dns);
        }
        command += L" -- " + QuoteArgument(entry.path);
        if (!entry.arguments.empty()) {
            command += L" " + entry.arguments;
        }
    }
    return command;
}

std::wstring SafeShortcutName(std::wstring name) {
    for (wchar_t& character : name) {
        if (character < 32 || std::wcschr(L"<>:\"/\\|?*", character) != nullptr) {
            character = L'_';
        }
    }
    while (!name.empty() && (name.back() == L' ' || name.back() == L'.')) {
        name.pop_back();
    }
    return name.empty() ? L"Easy-Net 代理入口" : name;
}

bool SaveCurrentEntry(GuiState& state, bool show_feedback);

bool CreateDesktopShortcut(GuiState& state) {
    if (!SaveCurrentEntry(state, false) || state.editing_index >= state.entries.size()) {
        return false;
    }
    const auto& entry = state.entries[state.editing_index];
    if (entry.id.empty()) {
        MessageBoxW(state.dialog, L"无法为当前入口生成唯一标识。", L"创建失败",
                    MB_OK | MB_ICONERROR);
        return false;
    }

    PWSTR desktop_raw = nullptr;
    const HRESULT folder_result = SHGetKnownFolderPath(FOLDERID_Desktop, KF_FLAG_DEFAULT,
                                                        nullptr, &desktop_raw);
    if (FAILED(folder_result) || desktop_raw == nullptr) {
        if (desktop_raw != nullptr) CoTaskMemFree(desktop_raw);
        MessageBoxW(state.dialog, L"无法确定当前用户的桌面目录。", L"创建失败",
                    MB_OK | MB_ICONERROR);
        return false;
    }
    const std::filesystem::path shortcut_path =
        std::filesystem::path(desktop_raw) / (SafeShortcutName(entry.name) + L"（代理）.lnk");
    CoTaskMemFree(desktop_raw);

    std::error_code exists_error;
    if (std::filesystem::exists(shortcut_path, exists_error) &&
        MessageBoxW(state.dialog,
                    (L"桌面上已存在同名快捷方式：\r\n" + shortcut_path.wstring() +
                     L"\r\n\r\n是否替换？")
                        .c_str(),
                    L"替换快捷方式", MB_YESNO | MB_ICONQUESTION | MB_DEFBUTTON2) != IDYES) {
        return false;
    }

    const HRESULT initialize_result = CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);
    const bool uninitialize = SUCCEEDED(initialize_result);
    if (FAILED(initialize_result) && initialize_result != RPC_E_CHANGED_MODE) {
        MessageBoxW(state.dialog, L"无法初始化 Windows 快捷方式服务。", L"创建失败",
                    MB_OK | MB_ICONERROR);
        return false;
    }

    IShellLinkW* shell_link = nullptr;
    HRESULT result = CoCreateInstance(CLSID_ShellLink, nullptr, CLSCTX_INPROC_SERVER,
                                      IID_PPV_ARGS(&shell_link));
    if (SUCCEEDED(result)) result = shell_link->SetPath(state.launcher_path.c_str());
    const std::wstring arguments = L"--launch-entry " + QuoteArgument(entry.id);
    if (SUCCEEDED(result)) result = shell_link->SetArguments(arguments.c_str());
    const std::filesystem::path working_directory =
        std::filesystem::path(state.launcher_path).parent_path();
    if (SUCCEEDED(result)) result = shell_link->SetWorkingDirectory(working_directory.c_str());
    if (SUCCEEDED(result)) result = shell_link->SetShowCmd(SW_HIDE);
    const std::wstring description = L"通过 Easy-Net Hook 代理启动 " + entry.name;
    if (SUCCEEDED(result)) result = shell_link->SetDescription(description.c_str());
    const std::filesystem::path icon_path =
        !entry.path.empty() && std::filesystem::is_regular_file(entry.path)
            ? std::filesystem::path(entry.path)
            : std::filesystem::path(state.launcher_path);
    if (SUCCEEDED(result)) result = shell_link->SetIconLocation(icon_path.c_str(), 0);

    IPersistFile* persist = nullptr;
    if (SUCCEEDED(result)) {
        result = shell_link->QueryInterface(IID_PPV_ARGS(&persist));
    }
    if (SUCCEEDED(result)) {
        result = persist->Save(shortcut_path.c_str(), TRUE);
    }
    if (persist != nullptr) persist->Release();
    if (shell_link != nullptr) shell_link->Release();
    if (uninitialize) CoUninitialize();

    if (FAILED(result)) {
        MessageBoxW(state.dialog,
                    (L"无法写入桌面快捷方式。\r\n错误代码：" +
                     std::to_wstring(static_cast<unsigned long>(result)))
                        .c_str(),
                    L"创建失败", MB_OK | MB_ICONERROR);
        SetControlText(state.dialog, IDC_STATUS, L"桌面快捷方式创建失败");
        return false;
    }

    SetControlText(state.dialog, IDC_ENTRY_STATE, L"快捷方式已创建：" + entry.name);
    SetControlText(state.dialog, IDC_STATUS,
                   L"以后双击桌面图标即可按此入口的最新设置启动");
    MessageBoxW(state.dialog,
                (L"已创建桌面快捷方式：\r\n" + shortcut_path.wstring() +
                 L"\r\n\r\n它会读取此入口的最新设置。使用时请保持 Easy-Net Hook "
                 L"程序目录位置不变，并先启动 SOCKS5 服务。")
                    .c_str(),
                L"快捷方式已创建", MB_OK | MB_ICONINFORMATION);
    return true;
}

bool SaveCurrentEntry(GuiState& state, bool show_feedback) {
    auto entry = CurrentEntry(state);
    if (!ValidateEntry(state.dialog, entry)) {
        return false;
    }
    const auto previous_entries = state.entries;
    const std::size_t previous_index = state.editing_index;
    const std::size_t saved_index = easy_net::history::SaveEntry(
        state.entries, std::move(entry), state.editing_index);
    state.editing_index = saved_index;
    if (!SaveEntries(state)) {
        state.entries = previous_entries;
        state.editing_index = previous_index;
        MessageBoxW(state.dialog,
                    L"入口无法保存到本机配置目录。请检查磁盘空间和目录权限。",
                    L"保存失败", MB_OK | MB_ICONERROR);
        SetControlText(state.dialog, IDC_STATUS, L"入口保存失败");
        return false;
    }
    state.dirty = false;
    RefreshEntries(state, saved_index);
    if (show_feedback) {
        SetControlText(state.dialog, IDC_ENTRY_STATE,
                       L"已保存：" + state.entries[saved_index].name);
        SetControlText(state.dialog, IDC_STATUS, L"入口已保存，下次可直接双击启动");
    }
    return true;
}

bool ConfirmDiscardChanges(GuiState& state) {
    if (!state.dirty) {
        return true;
    }
    const int choice = MessageBoxW(
        state.dialog,
        L"当前入口有尚未保存的修改。\r\n\r\n选择“是”保存修改，“否”放弃修改，"
        L"“取消”继续编辑。",
        L"保存入口修改", MB_YESNOCANCEL | MB_ICONQUESTION | MB_DEFBUTTON1);
    if (choice == IDYES) {
        return SaveCurrentEntry(state, false);
    }
    if (choice == IDNO) {
        state.dirty = false;
        return true;
    }
    return false;
}

std::wstring LaunchSuccessText(const easy_net::history::Entry& entry) {
    if (entry.mode == kModeChatGpt) {
        return L"ChatGPT 已启动。现在可以关闭 Easy-Net Hook，不影响 ChatGPT 使用。";
    }
    if (entry.mode == kModeAntigravity) {
        return L"Antigravity 已启动。关闭本窗口不会停止 IDE 或后台 language server 监视器。";
    }
    if (entry.mode == kModeCursor) {
        return L"Cursor 已启动；页面和 AI/扩展服务均已接管。关闭本窗口不影响使用。";
    }
    if (entry.mode == kModeWeChatWinDivert) {
        return L"微信 WinDivert 代理已启动。关闭本窗口不影响代理，微信退出后守护器自动停止。";
    }
    if (entry.mode == kModeWeChat) {
        return L"微信 TUN 代理已启动。关闭本窗口不影响代理，微信退出后 TUN 自动停止。";
    }
    return L"程序已通过代理启动。关闭本窗口不影响已注入的目标进程。";
}

void SetLaunchingUi(GuiState& state, bool launching) {
    state.launching = launching;
    for (const int identifier : {IDC_LAUNCH, IDC_SAVE_ENTRY, IDC_CREATE_SHORTCUT,
                                 IDC_NEW_ENTRY, IDC_REMOVE_ENTRY, IDC_ENTRIES}) {
        EnableWindow(GetDlgItem(state.dialog, identifier), !launching);
    }
    if (!launching && state.editing_index >= state.entries.size()) {
        EnableWindow(GetDlgItem(state.dialog, IDC_REMOVE_ENTRY), FALSE);
    }
    SetWindowTextW(GetDlgItem(state.dialog, IDC_LAUNCH),
                   launching ? L"启动中…" : L"保存并启动");
}

void ReadLaunchOutput(GuiState& state) {
    if (state.launch_output == nullptr) {
        return;
    }
    for (;;) {
        DWORD available = 0;
        if (!PeekNamedPipe(state.launch_output, nullptr, 0, nullptr, &available, nullptr) ||
            available == 0) {
            break;
        }
        char buffer[2048];
        DWORD read = 0;
        const DWORD wanted = (std::min)(available, static_cast<DWORD>(sizeof(buffer)));
        if (!ReadFile(state.launch_output, buffer, wanted, &read, nullptr) || read == 0) {
            break;
        }
        if (state.launch_output_bytes.size() < kMaximumLaunchOutput) {
            const std::size_t remaining = kMaximumLaunchOutput - state.launch_output_bytes.size();
            state.launch_output_bytes.append(buffer, (std::min)(remaining,
                                                                  static_cast<std::size_t>(read)));
        }
    }
}

std::wstring ExitCodeExplanation(DWORD exit_code) {
    switch (exit_code) {
        case 2: return L"启动参数或代理/DNS 地址无效。";
        case 3: return L"找不到程序、Hook DLL 或所需文件。";
        case 4: return L"无法准备运行环境，或缺少 TUN/WinDivert 引擎。";
        case 5: return L"启动、注入或管理员授权失败。";
        case 7: return L"微信代理守护器状态异常或已停止。";
        default: return L"启动器执行失败。";
    }
}

void FinishLaunch(GuiState& state, DWORD exit_code) {
    KillTimer(state.dialog, kLaunchTimer);
    ReadLaunchOutput(state);
    if (state.launch_process != nullptr) {
        CloseHandle(state.launch_process);
        state.launch_process = nullptr;
    }
    if (state.launch_output != nullptr) {
        CloseHandle(state.launch_output);
        state.launch_output = nullptr;
    }
    SetLaunchingUi(state, false);

    const auto& entry = state.pending_entry;
    if (exit_code != 0) {
        if (exit_code == 6 && entry.mode == kModeAntigravity && !entry.isolated) {
            MessageBoxW(state.dialog,
                        L"检测到 Antigravity 仍在运行。请从托盘完全退出全部 Antigravity "
                        L"进程后重试，或者勾选“使用独立配置”。",
                        L"请先退出 Antigravity", MB_OK | MB_ICONWARNING);
        } else if (exit_code == 6 && entry.mode == kModeCursor && !entry.isolated) {
            MessageBoxW(state.dialog,
                        L"检测到当前 Cursor 不是由相同代理启动。请完全退出 Cursor 后重试，"
                        L"或者勾选“使用独立配置”。",
                        L"请先退出 Cursor", MB_OK | MB_ICONWARNING);
        } else if (exit_code == 6 && IsWeChatMode(entry.mode)) {
            MessageBoxW(state.dialog,
                        entry.wechat_existing
                            ? L"没有检测到可接管的微信主进程。请先正常打开微信，然后重试。"
                            : L"检测到微信主进程仍在运行。请接管已运行微信，或完全退出后重试。",
                        L"无法启动微信代理", MB_OK | MB_ICONWARNING);
        } else {
            std::wstring message = ExitCodeExplanation(exit_code);
            const std::wstring detail = DecodeProcessOutput(state.launch_output_bytes);
            if (!detail.empty()) {
                message += L"\r\n\r\n详细信息：\r\n" + detail.substr(0, 1600);
            } else {
                message += L"\r\n错误代码：" + std::to_wstring(exit_code);
            }
            MessageBoxW(state.dialog, message.c_str(), L"启动失败", MB_OK | MB_ICONERROR);
        }
        SetControlText(state.dialog, IDC_STATUS, L"启动失败，请查看上面的详细信息");
        state.launch_output_bytes.clear();
        return;
    }

    const std::wstring success = LaunchSuccessText(entry);
    SetControlText(state.dialog, IDC_ENTRY_STATE, L"已启动：" + entry.name);
    SetControlText(state.dialog, IDC_STATUS, success);
    state.launch_output_bytes.clear();
    if (IsDlgButtonChecked(state.dialog, IDC_CLOSE_AFTER_LAUNCH) == BST_CHECKED) {
        EndDialog(state.dialog, 0);
    }
}

bool LaunchEntry(GuiState& state) {
    if (state.launching) {
        return false;
    }
    auto entry = CurrentEntry(state);
    if (!ValidateEntry(state.dialog, entry)) {
        return false;
    }
    const auto previous_entries = state.entries;
    const std::size_t previous_index = state.editing_index;
    const std::size_t saved_index = easy_net::history::SaveEntry(
        state.entries, entry, state.editing_index);
    state.editing_index = saved_index;
    if (!SaveEntries(state)) {
        state.entries = previous_entries;
        state.editing_index = previous_index;
        MessageBoxW(state.dialog,
                    L"入口无法保存，因此尚未启动程序。请检查磁盘空间和目录权限。",
                    L"保存失败", MB_OK | MB_ICONERROR);
        SetControlText(state.dialog, IDC_STATUS, L"入口保存失败，尚未启动");
        return false;
    }
    state.dirty = false;
    RefreshEntries(state, saved_index);
    std::wstring command_line = BuildLauncherCommand(state.launcher_path, entry, true);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');
    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    startup.dwFlags = STARTF_USESHOWWINDOW | STARTF_USESTDHANDLES;
    startup.wShowWindow = SW_HIDE;
    SECURITY_ATTRIBUTES security{};
    security.nLength = sizeof(security);
    security.bInheritHandle = TRUE;
    HANDLE output_read = nullptr;
    HANDLE output_write = nullptr;
    if (!CreatePipe(&output_read, &output_write, &security, 0) ||
        !SetHandleInformation(output_read, HANDLE_FLAG_INHERIT, 0)) {
        if (output_read != nullptr) CloseHandle(output_read);
        if (output_write != nullptr) CloseHandle(output_write);
        MessageBoxW(state.dialog, L"无法创建启动诊断管道。", L"启动失败",
                    MB_OK | MB_ICONERROR);
        return false;
    }
    startup.hStdInput = GetStdHandle(STD_INPUT_HANDLE);
    startup.hStdOutput = output_write;
    startup.hStdError = output_write;
    HANDLE null_input = CreateFileW(L"NUL", GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE,
                                    &security, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (null_input != INVALID_HANDLE_VALUE) {
        startup.hStdInput = null_input;
    } else {
        startup.hStdInput = nullptr;
    }
    PROCESS_INFORMATION process{};
    if (!CreateProcessW(state.launcher_path.c_str(), mutable_command.data(), nullptr, nullptr,
                        TRUE, CREATE_NO_WINDOW, nullptr, nullptr, &startup, &process)) {
        CloseHandle(output_read);
        CloseHandle(output_write);
        if (null_input != INVALID_HANDLE_VALUE) CloseHandle(null_input);
        MessageBoxW(state.dialog, L"无法启动 Easy-Net Hook 子进程。", L"启动失败",
                    MB_OK | MB_ICONERROR);
        return false;
    }
    if (null_input != INVALID_HANDLE_VALUE) CloseHandle(null_input);
    CloseHandle(output_write);
    CloseHandle(process.hThread);
    state.pending_entry = std::move(entry);
    state.launch_process = process.hProcess;
    state.launch_output = output_read;
    state.launch_output_bytes.clear();
    state.launch_started_tick = GetTickCount64();
    SetLaunchingUi(state, true);
    SetControlText(state.dialog, IDC_STATUS,
                   IsWeChatMode(state.pending_entry.mode)
                       ? L"正在启动微信代理；如弹出 UAC，请确认管理员授权…"
                       : L"正在启动并检查代理配置…");
    SetTimer(state.dialog, kLaunchTimer, 100, nullptr);
    return true;
}

void InitializeList(GuiState& state) {
    const HWND list = GetDlgItem(state.dialog, IDC_ENTRIES);
    ListView_SetExtendedListViewStyle(list, LVS_EX_DOUBLEBUFFER | LVS_EX_LABELTIP |
                                               LVS_EX_BORDERSELECT);
    struct Column {
        const wchar_t* name;
        int width;
    };
    while (ListView_DeleteColumn(list, 0)) {
    }
    constexpr Column columns[]{{L"名称", 150}, {L"模式", 105}, {L"代理", 145}};
    for (int index = 0; index < static_cast<int>(std::size(columns)); ++index) {
        LVCOLUMNW column{};
        column.mask = LVCF_TEXT | LVCF_WIDTH | LVCF_SUBITEM;
        column.pszText = const_cast<wchar_t*>(columns[index].name);
        column.cx = ScaleForDpi(state.dialog, columns[index].width);
        column.iSubItem = index;
        ListView_InsertColumn(list, index, &column);
    }
    const auto scale = [&state](int value) { return ScaleForDpi(state.dialog, value); };
    state.entry_images = ImageList_Create(scale(28), scale(28), ILC_COLOR32 | ILC_MASK, 8, 8);
    if (state.entry_images != nullptr) {
        ListView_SetImageList(list, state.entry_images, LVSIL_NORMAL);
    }
    ListView_SetView(list, LV_VIEW_TILE);
    LVTILEVIEWINFO tile_view{};
    tile_view.cbSize = sizeof(tile_view);
    tile_view.dwMask = LVTVIM_TILESIZE | LVTVIM_COLUMNS | LVTVIM_LABELMARGIN;
    tile_view.dwFlags = LVTVIF_FIXEDSIZE;
    tile_view.sizeTile.cx = scale(178);
    tile_view.sizeTile.cy = scale(52);
    tile_view.cLines = 2;
    tile_view.rcLabelMargin = {scale(6), scale(3), scale(4), scale(3)};
    ListView_SetTileViewInfo(list, &tile_view);
}

void CenterDialog(HWND dialog) {
    RECT window{};
    GetWindowRect(dialog, &window);
    MONITORINFO monitor_info{};
    monitor_info.cbSize = sizeof(monitor_info);
    const POINT cursor = [] {
        POINT point{};
        GetCursorPos(&point);
        return point;
    }();
    const HMONITOR monitor = MonitorFromPoint(cursor, MONITOR_DEFAULTTONEAREST);
    GetMonitorInfoW(monitor, &monitor_info);
    const RECT work_area = monitor_info.rcWork;
    const int width = window.right - window.left;
    const int height = window.bottom - window.top;
    const int x = work_area.left + (work_area.right - work_area.left - width) / 2;
    const int y = work_area.top + (work_area.bottom - work_area.top - height) / 2;
    SetWindowPos(dialog, nullptr, x, y, 0, 0, SWP_NOSIZE | SWP_NOZORDER);
}

INT_PTR CALLBACK DialogProcedure(HWND dialog, UINT message, WPARAM word_parameter,
                                 LPARAM long_parameter) {
    if (message == WM_INITDIALOG) {
        auto* state = reinterpret_cast<GuiState*>(long_parameter);
        state->dialog = dialog;
        SetWindowLongPtrW(dialog, DWLP_USER, reinterpret_cast<LONG_PTR>(state));
        state->entries_path = StoragePath(L"launcher-entries.tsv");
        std::error_code storage_error;
        const bool entries_exist = !state->entries_path.empty() &&
                                   std::filesystem::exists(state->entries_path, storage_error);
        state->entries = LoadHistory(state->entries_path);
        if (!entries_exist) {
            const auto legacy_path = StoragePath(L"launcher-history.tsv");
            state->entries = LoadHistory(legacy_path);
            if (!state->entries.empty()) {
                SaveEntries(*state);
            }
        }
        if (EnsureEntryIds(state->entries)) {
            SaveEntries(*state);
        }

        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"ChatGPT（桌面应用）"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"Antigravity IDE"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"Cursor（原生代理）"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"微信（TUN）"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"微信（WinDivert TCP+UDP）"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"通用程序 Hook"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_SETCURSEL, 0, 0);
        SetControlText(dialog, IDC_PROXY, L"127.0.0.1:1082");
        for (const wchar_t* label : {L"自动检测", L"通过代理", L"阻断", L"直接连接"}) {
            SendDlgItemMessageW(dialog, IDC_UDP_MODE, CB_ADDSTRING, 0,
                                reinterpret_cast<LPARAM>(label));
        }
        SendDlgItemMessageW(dialog, IDC_UDP_MODE, CB_SETCURSEL, 0, 0);
        CheckDlgButton(dialog, IDC_CLOSE_AFTER_LAUNCH, BST_CHECKED);
        SendDlgItemMessageW(dialog, IDC_PROXY, EM_SETCUEBANNER, TRUE,
                            reinterpret_cast<LPARAM>(L"127.0.0.1:1082"));
        SendDlgItemMessageW(dialog, IDC_DNS, EM_SETCUEBANNER, TRUE,
                            reinterpret_cast<LPARAM>(L"留空使用 Windows DNS"));
        SendDlgItemMessageW(dialog, IDC_PATH, EM_SETCUEBANNER, TRUE,
                            reinterpret_cast<LPARAM>(L"留空时自动查找（如支持）"));
        SendDlgItemMessageW(dialog, IDC_ARGUMENTS, EM_SETCUEBANNER, TRUE,
                            reinterpret_cast<LPARAM>(L"例如 --flag \"含空格的值\""));
        InitializeList(*state);
        RefreshEntries(*state, state->entries.empty() ?
                                    std::numeric_limits<std::size_t>::max() : 0);
        if (state->entries.empty()) {
            NewEntry(*state);
        } else {
            LoadEntry(*state, 0);
        }
        state->title_font = CreateFontW(-18, 0, 0, 0, FW_SEMIBOLD, FALSE, FALSE, FALSE,
                                        DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                        CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Segoe UI");
        if (state->title_font != nullptr) {
            SendDlgItemMessageW(dialog, IDC_TITLE, WM_SETFONT,
                                reinterpret_cast<WPARAM>(state->title_font), TRUE);
        }
        SendMessageW(dialog, WM_SETICON, ICON_SMALL,
                     reinterpret_cast<LPARAM>(LoadIconW(nullptr, IDI_APPLICATION)));
        CenterDialog(dialog);
        return TRUE;
    }

    GuiState* state = State(dialog);
    if (state == nullptr) {
        return FALSE;
    }
    switch (message) {
        case WM_DPICHANGED: {
            const auto* suggested = reinterpret_cast<const RECT*>(long_parameter);
            SetWindowPos(dialog, nullptr, suggested->left, suggested->top,
                         suggested->right - suggested->left, suggested->bottom - suggested->top,
                         SWP_NOACTIVATE | SWP_NOZORDER);
            if (state->entry_images != nullptr) {
                ListView_SetImageList(GetDlgItem(dialog, IDC_ENTRIES), nullptr, LVSIL_NORMAL);
                ImageList_Destroy(state->entry_images);
                state->entry_images = nullptr;
            }
            ClearIconCache(*state);
            InitializeList(*state);
            RefreshEntries(*state, state->editing_index);
            return TRUE;
        }
        case WM_TIMER:
            if (word_parameter == kLaunchTimer && state->launch_process != nullptr) {
                ReadLaunchOutput(*state);
                const DWORD wait = WaitForSingleObject(state->launch_process, 0);
                if (wait == WAIT_OBJECT_0) {
                    DWORD exit_code = 5;
                    GetExitCodeProcess(state->launch_process, &exit_code);
                    FinishLaunch(*state, exit_code);
                } else if (wait == WAIT_FAILED) {
                    FinishLaunch(*state, 5);
                } else if (GetTickCount64() - state->launch_started_tick >= 10000) {
                    SetControlText(dialog, IDC_STATUS,
                                   L"程序仍在启动；这不是成功提示，请继续等待或确认 UAC 窗口…");
                }
                return TRUE;
            }
            break;
        case WM_COMMAND: {
            const int identifier = LOWORD(word_parameter);
            const int notification = HIWORD(word_parameter);
            if (!state->updating &&
                ((notification == EN_CHANGE &&
                  (identifier == IDC_ENTRY_NAME || identifier == IDC_PROXY ||
                   identifier == IDC_PATH || identifier == IDC_ARGUMENTS ||
                   identifier == IDC_DNS)) ||
                 (notification == CBN_SELCHANGE &&
                  (identifier == IDC_MODE || identifier == IDC_UDP_MODE)) ||
                 (notification == BN_CLICKED &&
                  (identifier == IDC_ISOLATED || identifier == IDC_WECHAT_EXISTING)))) {
                state->dirty = true;
            }
            if (identifier == IDC_MODE && notification == CBN_SELCHANGE && !state->updating) {
                const std::wstring current_name = ControlText(dialog, IDC_ENTRY_NAME);
                if (IsDefaultEntryName(current_name)) {
                    const int mode = static_cast<int>(SendDlgItemMessageW(
                        dialog, IDC_MODE, CB_GETCURSEL, 0, 0));
                    SetControlText(dialog, IDC_ENTRY_NAME, DefaultEntryName(mode));
                }
                UpdateModeUi(*state);
                return TRUE;
            }
            if (identifier == IDC_WECHAT_EXISTING && notification == BN_CLICKED) {
                UpdateModeUi(*state);
                return TRUE;
            }
            if (identifier == IDC_BROWSE && notification == BN_CLICKED) {
                if (BrowseForExecutable(*state)) {
                    const std::wstring name = ControlText(dialog, IDC_ENTRY_NAME);
                    if (name.empty() || name == DefaultEntryName(5)) {
                        SetControlText(dialog, IDC_ENTRY_NAME,
                                       std::filesystem::path(ControlText(dialog, IDC_PATH))
                                           .stem().wstring());
                    }
                }
                return TRUE;
            }
            if (identifier == IDC_TEST_PROXY && notification == BN_CLICKED) {
                TestProxy(*state);
                return TRUE;
            }
            if (identifier == IDC_OPEN_LOGS && notification == BN_CLICKED) {
                OpenLogs(*state);
                return TRUE;
            }
            if (identifier == IDC_WECHAT_STATUS && notification == BN_CLICKED) {
                ShowWeChatStatus(*state);
                return TRUE;
            }
            if (identifier == IDC_NEW_ENTRY && notification == BN_CLICKED) {
                if (ConfirmDiscardChanges(*state)) {
                    NewEntry(*state);
                }
                return TRUE;
            }
            if (identifier == IDC_SAVE_ENTRY && notification == BN_CLICKED) {
                SaveCurrentEntry(*state, true);
                return TRUE;
            }
            if (identifier == IDC_CREATE_SHORTCUT && notification == BN_CLICKED) {
                CreateDesktopShortcut(*state);
                return TRUE;
            }
            if (identifier == IDC_LAUNCH && notification == BN_CLICKED) {
                LaunchEntry(*state);
                return TRUE;
            }
            if (identifier == IDC_REMOVE_ENTRY && notification == BN_CLICKED) {
                if (!ConfirmDiscardChanges(*state)) {
                    return TRUE;
                }
                const int selected = SelectedEntryIndex(dialog);
                if (selected >= 0 && static_cast<std::size_t>(selected) < state->entries.size() &&
                    MessageBoxW(dialog, L"确定删除这个快捷启动入口吗？", L"删除入口",
                                MB_YESNO | MB_ICONQUESTION | MB_DEFBUTTON2) == IDYES) {
                    const auto previous_entries = state->entries;
                    state->entries.erase(state->entries.begin() + selected);
                    if (!SaveEntries(*state)) {
                        state->entries = previous_entries;
                        MessageBoxW(dialog, L"无法保存删除结果，请检查配置目录权限。",
                                    L"删除失败", MB_OK | MB_ICONERROR);
                        return TRUE;
                    }
                    RefreshEntries(*state);
                    NewEntry(*state);
                }
                return TRUE;
            }
            if (identifier == IDCANCEL) {
                if (state->launching) {
                    if (MessageBoxW(dialog,
                                    L"程序仍在启动。关闭窗口只会停止显示启动结果，不会终止"
                                    L"已经创建或正在等待授权的进程。是否关闭？",
                                    L"仍在启动", MB_YESNO | MB_ICONWARNING |
                                                     MB_DEFBUTTON2) == IDYES) {
                        EndDialog(dialog, 0);
                    }
                } else if (ConfirmDiscardChanges(*state)) {
                    EndDialog(dialog, 0);
                }
                return TRUE;
            }
            break;
        }
        case WM_NOTIFY: {
            const auto* header = reinterpret_cast<NMHDR*>(long_parameter);
            if (header->idFrom == IDC_ENTRIES && header->code == LVN_KEYDOWN) {
                const auto* key = reinterpret_cast<NMLVKEYDOWN*>(long_parameter);
                if (key->wVKey == VK_RETURN) {
                    const int selected = SelectedEntryIndex(dialog);
                    if (selected >= 0) LaunchEntry(*state);
                    return TRUE;
                }
                if (key->wVKey == VK_DELETE) {
                    SendMessageW(dialog, WM_COMMAND,
                                 MAKEWPARAM(IDC_REMOVE_ENTRY, BN_CLICKED), 0);
                    return TRUE;
                }
            }
            if (header->idFrom == IDC_ENTRIES && header->code == LVN_ITEMCHANGED) {
                if (state->updating) {
                    return TRUE;
                }
                const auto* changed = reinterpret_cast<NMLISTVIEW*>(long_parameter);
                if ((changed->uNewState & LVIS_SELECTED) != 0 && changed->iItem >= 0) {
                    const std::size_t requested = static_cast<std::size_t>(changed->iItem);
                    if (requested != state->editing_index && !ConfirmDiscardChanges(*state)) {
                        state->updating = true;
                        ListView_SetItemState(GetDlgItem(dialog, IDC_ENTRIES), changed->iItem, 0,
                                              LVIS_SELECTED | LVIS_FOCUSED);
                        if (state->editing_index < state->entries.size()) {
                            ListView_SetItemState(GetDlgItem(dialog, IDC_ENTRIES),
                                                  static_cast<int>(state->editing_index),
                                                  LVIS_SELECTED | LVIS_FOCUSED,
                                                  LVIS_SELECTED | LVIS_FOCUSED);
                        }
                        state->updating = false;
                    } else if (!state->updating) {
                        LoadEntry(*state, requested);
                    }
                }
                return TRUE;
            }
            if (header->idFrom == IDC_ENTRIES && header->code == NM_DBLCLK) {
                const int selected = SelectedEntryIndex(dialog);
                if (selected >= 0) {
                    LaunchEntry(*state);
                }
                return TRUE;
            }
            break;
        }
        case WM_CLOSE:
            if (state->launching) {
                if (MessageBoxW(dialog,
                                L"程序仍在启动。关闭窗口只会停止显示启动结果，不会终止"
                                L"已经创建或正在等待授权的进程。是否关闭？",
                                L"仍在启动", MB_YESNO | MB_ICONWARNING | MB_DEFBUTTON2) ==
                    IDYES) {
                    EndDialog(dialog, 0);
                }
            } else if (ConfirmDiscardChanges(*state)) {
                EndDialog(dialog, 0);
            }
            return TRUE;
        case WM_DESTROY:
            KillTimer(dialog, kLaunchTimer);
            if (state->launch_process != nullptr) {
                CloseHandle(state->launch_process);
                state->launch_process = nullptr;
            }
            if (state->launch_output != nullptr) {
                CloseHandle(state->launch_output);
                state->launch_output = nullptr;
            }
            if (state->entry_images != nullptr) {
                ListView_SetImageList(GetDlgItem(dialog, IDC_ENTRIES), nullptr, LVSIL_NORMAL);
                ImageList_Destroy(state->entry_images);
                state->entry_images = nullptr;
            }
            ClearIconCache(*state);
            if (state->title_font != nullptr) {
                DeleteObject(state->title_font);
                state->title_font = nullptr;
            }
            return TRUE;
        default:
            break;
    }
    return FALSE;
}

}  // namespace

bool ResolveSavedEntryCommandLine(const std::wstring& launcher_path,
                                  const std::wstring& entry_id,
                                  std::wstring& command_line,
                                  std::wstring& entry_name) {
    if (entry_id.empty()) {
        return false;
    }
    const auto entries = LoadHistory(StoragePath(L"launcher-entries.tsv"));
    const auto entry = std::find_if(entries.begin(), entries.end(), [&entry_id](const auto& item) {
        return !item.id.empty() && _wcsicmp(item.id.c_str(), entry_id.c_str()) == 0;
    });
    if (entry == entries.end()) {
        return false;
    }
    // Desktop shortcuts are UI launches too: prevent GUI targets such as Cursor
    // from inheriting the launcher's hidden console handles.
    command_line = BuildLauncherCommand(launcher_path, *entry, true);
    entry_name = entry->name;
    return true;
}

int RunLauncherGui(HINSTANCE instance, const std::wstring& launcher_path) {
    INITCOMMONCONTROLSEX controls{};
    controls.dwSize = sizeof(controls);
    controls.dwICC = ICC_LISTVIEW_CLASSES | ICC_STANDARD_CLASSES;
    InitCommonControlsEx(&controls);

    GuiState state;
    state.instance = instance;
    state.launcher_path = launcher_path;
    const INT_PTR result = DialogBoxParamW(instance, MAKEINTRESOURCEW(IDD_LAUNCHER), nullptr,
                                           DialogProcedure, reinterpret_cast<LPARAM>(&state));
    if (result == -1) {
        MessageBoxW(nullptr, L"无法创建 Easy-Net Hook 启动器界面。", L"Easy-Net Hook",
                    MB_OK | MB_ICONERROR);
        return 1;
    }
    return static_cast<int>(result);
}
