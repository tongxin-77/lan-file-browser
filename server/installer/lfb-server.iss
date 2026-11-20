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
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM {#AppExe}"; Flags: runhidden; RunOnceId: "StopServer"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""{#RuleTCP}"""; Flags: runhidden; RunOnceId: "DelRuleTCP"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""{#RuleUDP}"""; Flags: runhidden; RunOnceId: "DelRuleUDP"

[Code]
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

// 卸载结束时询问是否删除配置与数据（默认保留，静默卸载时保留）
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
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
