; Installer for 192168.
;
; Lays down the client, the daemon, and the Wintun driver, then registers the
; daemon as a Windows service. Uninstalling stops the service, removes the
; network adapter, and deregisters it, so nothing is left behind.
;
; Build it with scripts\package.ps1, which compiles everything and fetches Wintun
; first. Compiling this file on its own fails on missing inputs.

#define AppName "192168"
#define AppPublisher "Tiago Mouta"
#define AppUrl "https://192168.lol"
#define ClientExe "Net192168.Client.exe"
#define ServiceExe "192168-service.exe"

; Passed in by package.ps1 so the version lives in one place.
#ifndef AppVersion
  #define AppVersion "0.1.0"
#endif

; Where package.ps1 staged everything.
#ifndef StageDir
  #define StageDir "..\build\stage"
#endif

[Setup]
AppId={{9F2C4A6E-1B3D-4E7A-9C58-192168000001}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppUrl}
AppSupportURL={#AppUrl}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
UninstallDisplayName={#AppName}
UninstallDisplayIcon={app}\{#ClientExe}
OutputDir=..\build
OutputBaseFilename=192168-{#AppVersion}-setup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
; The service is machine-wide and Program Files is not user-writable, so this
; needs administrator rights once, at install time.
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
ArchitecturesAllowed=x64compatible
; Nothing here runs on anything older, and the Windows App SDK is bundled.
MinVersion=10.0.19041
LicenseFile={#StageDir}\LICENSE.txt

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Shortcuts:"; Flags: unchecked

; No "start at sign-in" task here. Setup runs elevated, so the per-user Run key
; it would write belongs to whichever account approved the prompt. The checkbox
; in Settings writes it as the actual user.

[Files]
; The client is published self-contained, so the whole directory goes.
Source: "{#StageDir}\client\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#StageDir}\{#ServiceExe}"; DestDir: "{app}"; Flags: ignoreversion
; Wintun, with the licence it ships under.
Source: "{#StageDir}\wintun.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\wintun-LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\THIRD-PARTY-NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\{#ClientExe}"
Name: "{group}\Uninstall {#AppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#ClientExe}"; Tasks: desktopicon

[Run]
; The daemon registers itself, so there is one implementation of it.
Filename: "{app}\{#ServiceExe}"; Parameters: "service install"; \
  StatusMsg: "Registering the background service..."; Flags: runhidden waituntilterminated
Filename: "{app}\{#ClientExe}"; Description: "Open {#AppName}"; \
  Flags: nowait postinstall skipifsilent

[UninstallRun]
; Stops the service, removes the adapter, deregisters it. Runs before the files
; go, since it is one of them.
Filename: "{app}\{#ServiceExe}"; Parameters: "service uninstall"; \
  RunOnceId: "RemoveService"; Flags: runhidden waituntilterminated

[UninstallDelete]
; The service identity and log.
Type: filesandordirs; Name: "{commonappdata}\{#AppName}"

[Code]
// Both hold their own executables, and Windows gives a confusing "file in use"
// prompt rather than saying which.
procedure StopRunningInstances();
var
  ResultCode: Integer;
begin
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/IM {#ClientExe} /F', '',
    SW_HIDE, ewWaitUntilTerminated, ResultCode);
  // Only meaningful on an upgrade, where a previous version is installed.
  if FileExists(ExpandConstant('{app}\{#ServiceExe}')) then
    Exec(ExpandConstant('{app}\{#ServiceExe}'), 'service stop', '',
      SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  StopRunningInstances();
  Result := '';
end;

// Registering an already-registered service is an error, and the new daemon has
// to be the one Windows points at.
procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
begin
  if CurStep = ssInstall then
    if FileExists(ExpandConstant('{app}\{#ServiceExe}')) then
      Exec(ExpandConstant('{app}\{#ServiceExe}'), 'service uninstall', '',
        SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
begin
  if CurUninstallStep = usUninstall then
    Exec(ExpandConstant('{sys}\taskkill.exe'), '/IM {#ClientExe} /F', '',
      SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;
