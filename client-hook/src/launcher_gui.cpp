#include "launcher_gui.h"

#include <commctrl.h>
#include <commdlg.h>
#include <shellapi.h>

#include <cstdint>
#include <cstring>
#include <cwchar>
#include <filesystem>
#include <iterator>
#include <limits>
#include <string>
#include <system_error>
#include <utility>
#include <vector>

#include "history_store.h"
#include "resource.h"

namespace {

constexpr wchar_t kModeChatGpt[] = L"chatgpt";
constexpr wchar_t kModeAntigravity[] = L"antigravity";
constexpr wchar_t kModeCursor[] = L"cursor";
constexpr wchar_t kModeWeChat[] = L"wechat";
constexpr wchar_t kModeWeChatWinDivert[] = L"wechat-windivert";
constexpr wchar_t kModeHook[] = L"hook";

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
    HFONT title_font = nullptr;
    bool updating = false;
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

HICON EntryIcon(const easy_net::history::Entry& entry, bool& owned) {
    owned = false;
    if (!entry.path.empty()) {
        SHFILEINFOW information{};
        if (SHGetFileInfoW(entry.path.c_str(), 0, &information, sizeof(information),
                           SHGFI_ICON | SHGFI_LARGEICON) != 0 &&
            information.hIcon != nullptr) {
            owned = true;
            return information.hIcon;
        }
    }
    if (entry.mode == kModeWeChat || entry.mode == kModeWeChatWinDivert) {
        return LoadIconW(nullptr, IDI_SHIELD);
    }
    if (entry.mode == kModeChatGpt) {
        return LoadIconW(nullptr, IDI_INFORMATION);
    }
    return LoadIconW(nullptr, IDI_APPLICATION);
}

void RefreshEntries(GuiState& state, std::size_t selected_index =
                                             std::numeric_limits<std::size_t>::max()) {
    const HWND list = GetDlgItem(state.dialog, IDC_ENTRIES);
    ListView_DeleteAllItems(list);
    if (state.entry_images != nullptr) {
        ImageList_RemoveAll(state.entry_images);
    }
    for (std::size_t index = 0; index < state.entries.size(); ++index) {
        const auto& entry = state.entries[index];
        bool owned_icon = false;
        HICON icon = EntryIcon(entry, owned_icon);
        const int image_index = state.entry_images != nullptr && icon != nullptr
                                    ? ImageList_AddIcon(state.entry_images, icon)
                                    : -1;
        if (owned_icon && icon != nullptr) {
            DestroyIcon(icon);
        }
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
                       L"复用正常登录配置；首次需完全退出直连 Cursor。相同代理可继续开新窗口。\r\n"
                       L"独立配置可与现有 Cursor 并行，但需要单独登录。");
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

std::wstring BuildLauncherCommand(const GuiState& state,
                                  const easy_net::history::Entry& entry) {
    std::wstring command = QuoteArgument(state.launcher_path) + L" --proxy " +
                           QuoteArgument(entry.proxy) + L" --detach";
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
    RefreshEntries(state, saved_index);
    if (show_feedback) {
        SetControlText(state.dialog, IDC_ENTRY_STATE,
                       L"已保存：" + state.entries[saved_index].name);
        SetControlText(state.dialog, IDC_STATUS, L"入口已保存，下次可直接双击启动");
    }
    return true;
}

std::wstring LaunchSuccessText(const easy_net::history::Entry& entry) {
    if (entry.mode == kModeChatGpt) {
        return L"ChatGPT 已启动。现在可以关闭 Easy-Net Hook，不影响 ChatGPT 使用。";
    }
    if (entry.mode == kModeAntigravity) {
        return L"Antigravity 已启动。关闭本窗口不会停止 IDE 或后台 language server 监视器。";
    }
    if (entry.mode == kModeCursor) {
        return L"Cursor 已通过代理启动。关闭本窗口不影响 Cursor 使用。";
    }
    if (entry.mode == kModeWeChatWinDivert) {
        return L"微信 WinDivert 代理已启动。关闭本窗口不影响代理，微信退出后守护器自动停止。";
    }
    if (entry.mode == kModeWeChat) {
        return L"微信 TUN 代理已启动。关闭本窗口不影响代理，微信退出后 TUN 自动停止。";
    }
    return L"程序已通过代理启动。关闭本窗口不影响已注入的目标进程。";
}

bool LaunchEntry(GuiState& state) {
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
    RefreshEntries(state, saved_index);
    std::wstring command_line = BuildLauncherCommand(state, entry);
    std::vector<wchar_t> mutable_command(command_line.begin(), command_line.end());
    mutable_command.push_back(L'\0');
    STARTUPINFOW startup{};
    startup.cb = sizeof(startup);
    startup.dwFlags = STARTF_USESHOWWINDOW;
    startup.wShowWindow = SW_HIDE;
    PROCESS_INFORMATION process{};
    if (!CreateProcessW(state.launcher_path.c_str(), mutable_command.data(), nullptr, nullptr,
                        FALSE, CREATE_NO_WINDOW, nullptr, nullptr, &startup, &process)) {
        MessageBoxW(state.dialog, L"无法启动 Easy-Net Hook 子进程。", L"启动失败",
                    MB_OK | MB_ICONERROR);
        return false;
    }
    CloseHandle(process.hThread);
    const DWORD wait = WaitForSingleObject(process.hProcess, 10000);
    DWORD exit_code = 0;
    if (wait == WAIT_OBJECT_0) {
        GetExitCodeProcess(process.hProcess, &exit_code);
    }
    CloseHandle(process.hProcess);
    if (wait == WAIT_OBJECT_0 && exit_code != 0) {
        if (exit_code == 6 && entry.mode == kModeAntigravity && !entry.isolated) {
            MessageBoxW(state.dialog,
                        L"检测到 Antigravity 仍在运行。请从托盘完全退出全部 Antigravity "
                        L"进程后重试，确保新进程能够继承代理；或者勾选“使用独立配置”。",
                        L"请先退出 Antigravity", MB_OK | MB_ICONWARNING);
            SetControlText(state.dialog, IDC_STATUS, L"等待退出现有 Antigravity");
            return false;
        }
        if (exit_code == 6 && entry.mode == kModeCursor && !entry.isolated) {
            MessageBoxW(state.dialog,
                        L"检测到当前 Cursor 不是由相同的 Easy-Net 代理启动。请完全退出 "
                        L"Cursor 后重试，或者勾选“使用独立配置”。",
                        L"请先退出 Cursor", MB_OK | MB_ICONWARNING);
            SetControlText(state.dialog, IDC_STATUS, L"等待退出现有 Cursor");
            return false;
        }
        if (exit_code == 6 && IsWeChatMode(entry.mode)) {
            const wchar_t* message = entry.wechat_existing
                ? L"没有检测到可接管的微信进程。请先正常打开微信，然后重试。"
                : L"检测到微信仍在运行。请勾选“接管已经运行的微信”，或从托盘和任务管理器完全退出微信后重试。";
            MessageBoxW(state.dialog, message, L"无法启动微信代理",
                        MB_OK | MB_ICONWARNING);
            SetControlText(state.dialog, IDC_STATUS, L"等待退出微信");
            return false;
        }
        wchar_t message[160]{};
        swprintf_s(message, L"启动器返回错误代码 %lu。请检查程序路径、代理地址和运行权限。",
                   exit_code);
        MessageBoxW(state.dialog, message, L"启动失败", MB_OK | MB_ICONERROR);
        SetControlText(state.dialog, IDC_STATUS, L"启动失败");
        return false;
    }
    const std::wstring success = LaunchSuccessText(entry);
    SetControlText(state.dialog, IDC_ENTRY_STATE, L"已启动：" + entry.name);
    SetControlText(state.dialog, IDC_STATUS, success);
    if (IsDlgButtonChecked(state.dialog, IDC_CLOSE_AFTER_LAUNCH) == BST_CHECKED) {
        EndDialog(state.dialog, 0);
    }
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
    constexpr Column columns[]{{L"名称", 150}, {L"模式", 105}, {L"代理", 145}};
    for (int index = 0; index < static_cast<int>(std::size(columns)); ++index) {
        LVCOLUMNW column{};
        column.mask = LVCF_TEXT | LVCF_WIDTH | LVCF_SUBITEM;
        column.pszText = const_cast<wchar_t*>(columns[index].name);
        column.cx = columns[index].width;
        column.iSubItem = index;
        ListView_InsertColumn(list, index, &column);
    }
    state.entry_images = ImageList_Create(28, 28, ILC_COLOR32 | ILC_MASK, 8, 8);
    if (state.entry_images != nullptr) {
        ListView_SetImageList(list, state.entry_images, LVSIL_NORMAL);
    }
    ListView_SetView(list, LV_VIEW_TILE);
    LVTILEVIEWINFO tile_view{};
    tile_view.cbSize = sizeof(tile_view);
    tile_view.dwMask = LVTVIM_TILESIZE | LVTVIM_COLUMNS | LVTVIM_LABELMARGIN;
    tile_view.dwFlags = LVTVIF_FIXEDSIZE;
    tile_view.sizeTile.cx = 178;
    tile_view.sizeTile.cy = 52;
    tile_view.cLines = 2;
    tile_view.rcLabelMargin = {6, 3, 4, 3};
    ListView_SetTileViewInfo(list, &tile_view);
}

void CenterDialog(HWND dialog) {
    RECT window{};
    RECT work_area{};
    GetWindowRect(dialog, &window);
    SystemParametersInfoW(SPI_GETWORKAREA, 0, &work_area, 0);
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

        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"ChatGPT（推荐）"));
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
        for (const wchar_t* label : {L"自动检测（推荐）", L"通过代理", L"阻断", L"直接连接"}) {
            SendDlgItemMessageW(dialog, IDC_UDP_MODE, CB_ADDSTRING, 0,
                                reinterpret_cast<LPARAM>(label));
        }
        SendDlgItemMessageW(dialog, IDC_UDP_MODE, CB_SETCURSEL, 0, 0);
        CheckDlgButton(dialog, IDC_CLOSE_AFTER_LAUNCH, BST_CHECKED);
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
        case WM_COMMAND: {
            const int identifier = LOWORD(word_parameter);
            const int notification = HIWORD(word_parameter);
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
            if (identifier == IDC_NEW_ENTRY && notification == BN_CLICKED) {
                NewEntry(*state);
                return TRUE;
            }
            if (identifier == IDC_SAVE_ENTRY && notification == BN_CLICKED) {
                SaveCurrentEntry(*state, true);
                return TRUE;
            }
            if (identifier == IDC_LAUNCH && notification == BN_CLICKED) {
                LaunchEntry(*state);
                return TRUE;
            }
            if (identifier == IDC_REMOVE_ENTRY && notification == BN_CLICKED) {
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
                EndDialog(dialog, 0);
                return TRUE;
            }
            break;
        }
        case WM_NOTIFY: {
            const auto* header = reinterpret_cast<NMHDR*>(long_parameter);
            if (header->idFrom == IDC_ENTRIES && header->code == LVN_ITEMCHANGED) {
                const auto* changed = reinterpret_cast<NMLISTVIEW*>(long_parameter);
                if ((changed->uNewState & LVIS_SELECTED) != 0 && changed->iItem >= 0) {
                    LoadEntry(*state, static_cast<std::size_t>(changed->iItem));
                }
                return TRUE;
            }
            if (header->idFrom == IDC_ENTRIES && header->code == NM_DBLCLK) {
                const int selected = SelectedEntryIndex(dialog);
                if (selected >= 0) {
                    LoadEntry(*state, static_cast<std::size_t>(selected));
                    LaunchEntry(*state);
                }
                return TRUE;
            }
            break;
        }
        case WM_CLOSE:
            EndDialog(dialog, 0);
            return TRUE;
        case WM_DESTROY:
            if (state->entry_images != nullptr) {
                ListView_SetImageList(GetDlgItem(dialog, IDC_ENTRIES), nullptr, LVSIL_NORMAL);
                ImageList_Destroy(state->entry_images);
                state->entry_images = nullptr;
            }
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
