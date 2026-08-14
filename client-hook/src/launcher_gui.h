#pragma once

#include <winsock2.h>
#include <windows.h>

#include <string>

bool ResolveSavedEntryCommandLine(const std::wstring& launcher_path,
                                  const std::wstring& entry_id,
                                  std::wstring& command_line,
                                  std::wstring& entry_name);
int RunLauncherGui(HINSTANCE instance, const std::wstring& launcher_path);
