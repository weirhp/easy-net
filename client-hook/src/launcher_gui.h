#pragma once

#include <winsock2.h>
#include <windows.h>

#include <string>

int RunLauncherGui(HINSTANCE instance, const std::wstring& launcher_path);
