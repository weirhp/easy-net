#include "launcher_gui.h"

#include <commctrl.h>
#include <commdlg.h>

#include <cstdint>
#include <cstring>
#include <cwchar>
#include <filesystem>
#include <iterator>
#include <string>
#include <system_error>
#include <utility>
#include <vector>

#include "history_store.h"
#include "resource.h"

namespace {

constexpr wchar_t kModeChatGpt[] = L"chatgpt";
constexpr wchar_t kModeAntigravity[] = L"antigravity";
constexpr wchar_t kModeWeChat[] = L"wechat";
constexpr wchar_t kModeHook[] = L"hook";

struct GuiState {
    HINSTANCE instance = nullptr;
    HWND dialog = nullptr;
    std::wstring launcher_path;
    std::filesystem::path history_path;
    std::vector<easy_net::history::Entry> history;
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

std::filesystem::path HistoryPath() {
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
    return std::filesystem::path(local_app_data) / L"EasyNetHook" / L"launcher-history.tsv";
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

bool SaveHistory(const GuiState& state) {
    if (state.history_path.empty()) {
        return false;
    }
    std::error_code error;
    std::filesystem::create_directories(state.history_path.parent_path(), error);
    if (error) {
        return false;
    }
    HANDLE file = CreateFileW(state.history_path.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_ALWAYS,
                              FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) {
        return false;
    }
    const std::wstring content = easy_net::history::Serialize(state.history);
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
        return 3;
    }
    if (mode == kModeWeChat) {
        return 2;
    }
    return 0;
}

const wchar_t* ModeValue(int index) {
    if (index == 1) {
        return kModeAntigravity;
    }
    if (index == 2) {
        return kModeWeChat;
    }
    if (index == 3) {
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
    if (mode == kModeWeChat) {
        return L"微信 TUN";
    }
    return L"ChatGPT";
}

void RefreshHistory(GuiState& state) {
    const HWND list = GetDlgItem(state.dialog, IDC_HISTORY);
    ListView_DeleteAllItems(list);
    for (std::size_t index = 0; index < state.history.size(); ++index) {
        const auto& entry = state.history[index];
        LVITEMW item{};
        item.mask = LVIF_TEXT | LVIF_PARAM;
        item.iItem = static_cast<int>(index);
        item.pszText = const_cast<wchar_t*>(entry.name.c_str());
        item.lParam = static_cast<LPARAM>(index);
        const int row = ListView_InsertItem(list, &item);
        ListView_SetItemText(list, row, 1,
                             const_cast<wchar_t*>(ModeLabel(entry.mode)));
        ListView_SetItemText(list, row, 2, const_cast<wchar_t*>(entry.proxy.c_str()));
        ListView_SetItemText(list, row, 3,
                             const_cast<wchar_t*>(entry.last_used.c_str()));
    }
}

void UpdateModeUi(GuiState& state) {
    const int mode = static_cast<int>(SendDlgItemMessageW(state.dialog, IDC_MODE, CB_GETCURSEL,
                                                           0, 0));
    const bool chatgpt = mode == 0;
    const bool antigravity = mode == 1;
    const bool wechat = mode == 2;
    EnableWindow(GetDlgItem(state.dialog, IDC_PATH), !chatgpt);
    EnableWindow(GetDlgItem(state.dialog, IDC_BROWSE), !chatgpt);
    EnableWindow(GetDlgItem(state.dialog, IDC_ARGUMENTS), !chatgpt);
    EnableWindow(GetDlgItem(state.dialog, IDC_DNS), !chatgpt);
    ShowWindow(GetDlgItem(state.dialog, IDC_ISOLATED), antigravity ? SW_SHOW : SW_HIDE);
    EnableWindow(GetDlgItem(state.dialog, IDC_ISOLATED), antigravity);
    ShowWindow(GetDlgItem(state.dialog, IDC_UDP_LABEL), wechat ? SW_SHOW : SW_HIDE);
    ShowWindow(GetDlgItem(state.dialog, IDC_UDP_MODE), wechat ? SW_SHOW : SW_HIDE);
    EnableWindow(GetDlgItem(state.dialog, IDC_UDP_MODE), wechat);
    if (chatgpt) {
        SetControlText(state.dialog, IDC_PATH, L"");
        SetControlText(state.dialog, IDC_HINT,
                       L"隔离配置启动；ChatGPT 界面与 codex.exe 后端都通过代理。\r\n"
                       L"无需 DLL 注入。首次启动可能需要稍候。");
    } else if (mode == 1) {
        SetControlText(state.dialog, IDC_HINT,
                       L"默认复用桌面版登录状态；启动前请完全退出 Antigravity。\r\n"
                       L"独立配置可并行运行，但需要单独登录；language server 有兜底 Hook。");
    } else if (wechat) {
        SetControlText(state.dialog, IDC_HINT,
                       L"使用按进程 TUN 代理微信 TCP/UDP；需要管理员权限和 x64-TUN 包。\r\n"
                       L"程序路径可留空自动查找；启动前请完全退出微信。 ");
    } else {
        SetControlText(state.dialog, IDC_HINT,
                       L"适合普通 Win32 程序。通过 DLL Hook 代理 TCP；可选自定义 DNS。\r\n"
                       L"受保护进程或特殊网络栈可能不兼容。");
    }
}

void LoadEntry(GuiState& state, std::size_t index) {
    if (index >= state.history.size()) {
        return;
    }
    state.updating = true;
    const auto& entry = state.history[index];
    SendDlgItemMessageW(state.dialog, IDC_MODE, CB_SETCURSEL, ModeIndex(entry.mode), 0);
    SetControlText(state.dialog, IDC_PROXY, entry.proxy);
    SetControlText(state.dialog, IDC_PATH, entry.path);
    SetControlText(state.dialog, IDC_ARGUMENTS, entry.arguments);
    SetControlText(state.dialog, IDC_DNS, entry.dns);
    CheckDlgButton(state.dialog, IDC_ISOLATED,
                   entry.isolated ? BST_CHECKED : BST_UNCHECKED);
    int udp_index = 0;
    if (entry.udp_mode == L"proxy") udp_index = 1;
    else if (entry.udp_mode == L"block") udp_index = 2;
    else if (entry.udp_mode == L"direct") udp_index = 3;
    SendDlgItemMessageW(state.dialog, IDC_UDP_MODE, CB_SETCURSEL, udp_index, 0);
    UpdateModeUi(state);
    if (entry.mode != kModeChatGpt) {
        SetControlText(state.dialog, IDC_PATH, entry.path);
    }
    state.updating = false;
}

int SelectedHistoryIndex(HWND dialog) {
    return ListView_GetNextItem(GetDlgItem(dialog, IDC_HISTORY), -1, LVNI_SELECTED);
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
    entry.path = ControlText(state.dialog, IDC_PATH);
    entry.arguments = ControlText(state.dialog, IDC_ARGUMENTS);
    entry.proxy = ControlText(state.dialog, IDC_PROXY);
    entry.dns = ControlText(state.dialog, IDC_DNS);
    entry.isolated = mode == 1 &&
                     IsDlgButtonChecked(state.dialog, IDC_ISOLATED) == BST_CHECKED;
    if (mode == 2) {
        const int udp_mode = static_cast<int>(SendDlgItemMessageW(
            state.dialog, IDC_UDP_MODE, CB_GETCURSEL, 0, 0));
        constexpr const wchar_t* values[]{L"auto", L"proxy", L"block", L"direct"};
        entry.udp_mode = udp_mode >= 0 && udp_mode < 4 ? values[udp_mode] : L"auto";
    }
    entry.last_used = CurrentTimestamp();
    if (mode == 0) {
        entry.name = L"ChatGPT";
    } else if (mode == 1) {
        entry.name = L"Antigravity IDE";
    } else if (mode == 2) {
        entry.name = L"微信";
    } else if (!entry.path.empty()) {
        entry.name = std::filesystem::path(entry.path).stem().wstring();
    } else {
        entry.name = L"未命名程序";
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
    } else if (entry.mode == kModeWeChat) {
        command += L" --wechat --tun-udp " +
                   QuoteArgument(entry.udp_mode.empty() ? L"auto" : entry.udp_mode);
        if (!entry.path.empty()) {
            command += L" --wechat-path " + QuoteArgument(entry.path);
        }
        if (!entry.dns.empty()) {
            command += L" --dns " + QuoteArgument(entry.dns);
        }
        if (!entry.arguments.empty()) {
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

bool LaunchEntry(GuiState& state) {
    auto entry = CurrentEntry(state);
    if (!ValidateEntry(state.dialog, entry)) {
        return false;
    }
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
        if (exit_code == 6 && entry.mode == kModeWeChat) {
            MessageBoxW(state.dialog,
                        L"检测到微信仍在运行。请从托盘或任务管理器完全退出微信及其子进程后重试，确保所有新连接进入 TUN。",
                        L"请先退出微信", MB_OK | MB_ICONWARNING);
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
    easy_net::history::Upsert(state.history, std::move(entry));
    SaveHistory(state);
    RefreshHistory(state);
    SetControlText(state.dialog, IDC_STATUS, L"已通过代理启动");
    return true;
}

void InitializeList(HWND list) {
    ListView_SetExtendedListViewStyle(list, LVS_EX_FULLROWSELECT | LVS_EX_DOUBLEBUFFER |
                                               LVS_EX_LABELTIP);
    struct Column {
        const wchar_t* name;
        int width;
    };
    constexpr Column columns[]{{L"名称", 105}, {L"模式", 78}, {L"代理", 98},
                               {L"最近使用", 120}};
    for (int index = 0; index < static_cast<int>(std::size(columns)); ++index) {
        LVCOLUMNW column{};
        column.mask = LVCF_TEXT | LVCF_WIDTH | LVCF_SUBITEM;
        column.pszText = const_cast<wchar_t*>(columns[index].name);
        column.cx = columns[index].width;
        column.iSubItem = index;
        ListView_InsertColumn(list, index, &column);
    }
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
        state->history_path = HistoryPath();
        state->history = LoadHistory(state->history_path);

        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"ChatGPT（推荐）"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"Antigravity IDE"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"微信（TUN）"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_ADDSTRING, 0,
                            reinterpret_cast<LPARAM>(L"通用程序 Hook"));
        SendDlgItemMessageW(dialog, IDC_MODE, CB_SETCURSEL, 0, 0);
        SetControlText(dialog, IDC_PROXY, L"127.0.0.1:1082");
        for (const wchar_t* label : {L"自动检测（推荐）", L"通过代理", L"阻断", L"直接连接"}) {
            SendDlgItemMessageW(dialog, IDC_UDP_MODE, CB_ADDSTRING, 0,
                                reinterpret_cast<LPARAM>(label));
        }
        SendDlgItemMessageW(dialog, IDC_UDP_MODE, CB_SETCURSEL, 0, 0);
        InitializeList(GetDlgItem(dialog, IDC_HISTORY));
        RefreshHistory(*state);
        UpdateModeUi(*state);
        state->title_font = CreateFontW(-22, 0, 0, 0, FW_SEMIBOLD, FALSE, FALSE, FALSE,
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
                UpdateModeUi(*state);
                return TRUE;
            }
            if (identifier == IDC_BROWSE && notification == BN_CLICKED) {
                BrowseForExecutable(*state);
                return TRUE;
            }
            if (identifier == IDC_LAUNCH && notification == BN_CLICKED) {
                LaunchEntry(*state);
                return TRUE;
            }
            if (identifier == IDC_REMOVE_HISTORY && notification == BN_CLICKED) {
                const int selected = SelectedHistoryIndex(dialog);
                if (selected >= 0 && static_cast<std::size_t>(selected) < state->history.size()) {
                    state->history.erase(state->history.begin() + selected);
                    SaveHistory(*state);
                    RefreshHistory(*state);
                }
                return TRUE;
            }
            if (identifier == IDC_CLEAR_HISTORY && notification == BN_CLICKED) {
                if (!state->history.empty() &&
                    MessageBoxW(dialog, L"确定清空全部启动记录吗？", L"清空记录",
                                MB_YESNO | MB_ICONQUESTION | MB_DEFBUTTON2) == IDYES) {
                    state->history.clear();
                    SaveHistory(*state);
                    RefreshHistory(*state);
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
            if (header->idFrom == IDC_HISTORY && header->code == LVN_ITEMCHANGED) {
                const auto* changed = reinterpret_cast<NMLISTVIEW*>(long_parameter);
                if ((changed->uNewState & LVIS_SELECTED) != 0 && changed->iItem >= 0) {
                    LoadEntry(*state, static_cast<std::size_t>(changed->iItem));
                }
                return TRUE;
            }
            if (header->idFrom == IDC_HISTORY && header->code == NM_DBLCLK) {
                const int selected = SelectedHistoryIndex(dialog);
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
