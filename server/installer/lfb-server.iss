; LAN File Browser 服务端安装包（Inno Setup 6.5+，设计见 docs/02-release.md §6）
; 在 server/ 下先构建 dist\lfb-server.exe，再编译：
;   iscc /DAppVersion=1.2.0 installer\lfb-server.iss   → dist\lfb-server-setup-1.2.0.exe

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#define AppName "LAN File Browser"
#define AppExe "lfb-server.exe"
#define AdminURL "http://127.0.0.1:8686/"
#define RuleTCP "LAN File Browser (TCP 8686)"
#define RuleUDP "LAN File Browser (UDP 8687)"
; 服务端监听的退出事件，须与 cmd/server/shutdownevent.go 一致
#define ShutdownEvent "Local\LanFileBrowserShutdown"

[Setup]
; AppId 固定不变：同 AppId 覆盖安装即升级
AppId={{CE4D9CC3-B17E-49B8-BD2D-4EB88DC9D79C}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppName}
AppPublisherURL=https://github.com/tongxin-77/lan-file-browser
AppSupportURL=https://github.com/tongxin-77/lan-file-browser/issues
AppUpdatesURL=https://github.com/tongxin-77/lan-file-browser/releases
; 添加防火墙规则需要管理员
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
DefaultDirName={autopf}\LanFileBrowser
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
; 开机自启写 HKCU、卸载时可删除 %APPDATA% 数据，均针对安装的当前用户
UsedUserAreasWarning=no
CloseApplications=yes
RestartApplications=no
OutputDir=..\dist
OutputBaseFilename=lfb-server-setup-{#AppVersion}
SetupIconFile=lfb.ico
UninstallDisplayIcon={app}\{#AppExe}
WizardStyle=modern
Compression=lzma2
SolidCompression=yes

[Languages]
; 简体中文为 Inno Setup 非官方翻译，随仓库提供（来源 jrsoftware/issrc is-6_7_1 Files/Languages/Unofficial）
Name: "chs"; MessagesFile: "ChineseSimplified.isl"

[Tasks]
Name: "autostart"; Description: "开机时自动启动（当前用户）"; Flags: unchecked

[Files]
Source: "..\dist\{#AppExe}"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExe}"
Name: "{group}\打开管理页"; Filename: "{#AdminURL}"
Name: "{group}\卸载 {#AppName}"; Filename: "{uninstallexe}"

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "LanFileBrowser"; ValueData: """{app}\{#AppExe}"""; Tasks: autostart; Flags: uninsdeletevalue

[Run]
; 防火墙入站规则：先删后加，避免覆盖安装时重复
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""{#RuleTCP}"""; Flags: runhidden
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""{#RuleUDP}"""; Flags: runhidden
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall add rule name=""{#RuleTCP}"" dir=in action=allow protocol=TCP localport=8686 program=""{app}\{#AppExe}"" enable=yes"; StatusMsg: "正在添加防火墙规则…"; Flags: runhidden
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall add rule name=""{#RuleUDP}"" dir=in action=allow protocol=UDP localport=8687 program=""{app}\{#AppExe}"" enable=yes"; StatusMsg: "正在添加防火墙规则…"; Flags: runhidden
; 以安装者本人（非提升权限）运行，才能看到其映射的网络盘、使用其 %APPDATA%
Filename: "{app}\{#AppExe}"; Parameters: "-no-browser"; Description: "启动 {#AppName} 并打开管理页"; Flags: postinstall nowait skipifsilent runasoriginaluser; AfterInstall: OpenAdminPage

[UninstallRun]
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""{#RuleTCP}"""; Flags: runhidden; RunOnceId: "DelRuleTCP"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""{#RuleUDP}"""; Flags: runhidden; RunOnceId: "DelRuleUDP"

[Code]
const
  EVENT_MODIFY_STATE = $0002;

function OpenEvent(DesiredAccess: DWORD; InheritHandle: BOOL; Name: String): THandle;
  external 'OpenEventW@kernel32.dll stdcall';
function SetEvent(Event: THandle): BOOL;
  external 'SetEvent@kernel32.dll stdcall';
function CloseHandle(Handle: THandle): BOOL;
  external 'CloseHandle@kernel32.dll stdcall';

var
  ServerWasRunning: Boolean;

// 按进程名判断 lfb-server.exe 是否在运行（WMI 不可用时视为未运行）
function ServerRunning: Boolean;
var
  Locator, Wmi: Variant;
begin
  try
    Locator := CreateOleObject('WbemScripting.SWbemLocator');
    Wmi := Locator.ConnectServer('.', 'root\CIMV2');
    Result := Wmi.ExecQuery('SELECT ProcessId FROM Win32_Process WHERE Name = ''{#AppExe}''').Count > 0;
  except
    Result := False;
  end;
end;

// 等进程消失，最多 Ms 毫秒
function WaitServerExit(Ms: Integer): Boolean;
var
  I: Integer;
begin
  for I := 1 to Ms div 250 do
  begin
    if not ServerRunning then
    begin
      Result := True;
      Exit;
    end;
    Sleep(250);
  end;
  Result := not ServerRunning;
end;

// 关闭运行中的服务端：先置位退出事件请求优雅退出，未退出再 taskkill /F；返回空串表示成功
// （程序无窗口，CloseApplications 的 Restart Manager 关不掉它）
function StopServer: String;
var
  Event: THandle;
  Code: Integer;
begin
  Result := '';
  if not ServerRunning then
    Exit;
  ServerWasRunning := True;
  Event := OpenEvent(EVENT_MODIFY_STATE, False, '{#ShutdownEvent}');
  if Event <> 0 then
  begin
    SetEvent(Event);
    CloseHandle(Event);
    if WaitServerExit(8000) then
      Exit;
  end;
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM {#AppExe}', '', SW_HIDE, ewWaitUntilTerminated, Code);
  if not WaitServerExit(10000) then
    Result := '无法关闭正在运行的 {#AppName}（{#AppExe}）。请在任务管理器中结束该进程后重试。';
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := StopServer;
end;

// 静默安装（自动更新）时没有完成页的“启动”选项：之前在运行的，装完自动以原用户重新启动
procedure CurStepChanged(CurStep: TSetupStep);
var
  Code: Integer;
begin
  if (CurStep = ssPostInstall) and WizardSilent and ServerWasRunning then
    ExecAsOriginalUser(ExpandConstant('{app}\{#AppExe}'), '-no-browser', '', SW_HIDE, ewNoWait, Code);
end;

// 等服务端开始监听（最多约 10 秒）后再打开管理页，避免浏览器先于服务端打开而报错
procedure OpenAdminPage;
var
  Http: Variant;
  I, Code: Integer;
begin
  for I := 1 to 20 do
  begin
    try
      Http := CreateOleObject('WinHttp.WinHttpRequest.5.1');
      Http.SetTimeouts(500, 500, 500, 500);
      Http.Open('GET', '{#AdminURL}api/ping', False);
      Http.Send('');
      Break;
    except
      Sleep(500);
    end;
  end;
  ShellExecAsOriginalUser('open', '{#AdminURL}', '', '', SW_SHOWNORMAL, ewNoWait, Code);
end;

// 卸载开始前关闭服务端；卸载结束时询问是否删除配置与数据（默认保留，静默卸载时保留）
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Msg: String;
begin
  if CurUninstallStep = usUninstall then
  begin
    Msg := StopServer;
    if (Msg <> '') and not UninstallSilent then
      MsgBox(Msg + #13#10 + '卸载将继续，未能删除的文件可在重启后手动删除。', mbError, MB_OK);
  end;
  if (CurUninstallStep = usPostUninstall) and not UninstallSilent then
    if MsgBox('是否同时删除配置、已配对设备与观看记录等数据？' + #13#10 + #13#10 +
              ExpandConstant('{userappdata}\LanFileBrowser') + #13#10 +
              ExpandConstant('{localappdata}\LanFileBrowser') + #13#10 + #13#10 +
              '选择“否”将保留，重新安装后可继续使用。',
              mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
    begin
      DelTree(ExpandConstant('{userappdata}\LanFileBrowser'), True, True, True);
      DelTree(ExpandConstant('{localappdata}\LanFileBrowser'), True, True, True);
    end;
end;
