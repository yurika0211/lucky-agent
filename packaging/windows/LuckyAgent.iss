#ifndef SourceRoot
  #define SourceRoot "..\\..\\dist\\windows-installer"
#endif
#ifndef MyAppVersion
  #define MyAppVersion "dev"
#endif

[Setup]
AppId={{F7E5D335-EDC5-437B-9E6B-51FBBEA89A03}
AppName=LuckyAgent
AppVersion={#MyAppVersion}
AppPublisher=LuckyAgent
DefaultDirName={localappdata}\LuckyAgent
DefaultGroupName=LuckyAgent
DisableProgramGroupPage=yes
OutputBaseFilename=LuckyAgent-Setup-{#MyAppVersion}-x64
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=lowest
UninstallDisplayIcon={app}\lh.exe
ChangesEnvironment=yes

[Files]
Source: "{#SourceRoot}\lh.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceRoot}\ConfigurationCenter.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceRoot}\Install-Portable.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceRoot}\LuckyAgent-TUI.cmd"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceRoot}\LuckyAgent-GUI.cmd"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceRoot}\UI\GUI\dist\*"; DestDir: "{app}\UI\GUI\dist"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#SourceRoot}\UI\TUI\dist\*"; DestDir: "{app}\UI\TUI\dist"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#SourceRoot}\runtime\node\*"; DestDir: "{app}\runtime\node"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\LuckyAgent Configuration Center"; Filename: "powershell.exe"; Parameters: "-ExecutionPolicy Bypass -File ""{app}\ConfigurationCenter.ps1"""; WorkingDir: "{app}"
Name: "{group}\LuckyAgent GUI"; Filename: "{app}\LuckyAgent-GUI.cmd"; WorkingDir: "{app}"
Name: "{group}\LuckyAgent TUI"; Filename: "{app}\LuckyAgent-TUI.cmd"; WorkingDir: "{app}"
Name: "{group}\Stop LuckyAgent"; Filename: "powershell.exe"; Parameters: "-ExecutionPolicy Bypass -File ""{app}\ConfigurationCenter.ps1"" -Action Stop"; WorkingDir: "{app}"
Name: "{autodesktop}\LuckyAgent Configuration Center"; Filename: "powershell.exe"; Parameters: "-ExecutionPolicy Bypass -File ""{app}\ConfigurationCenter.ps1"""; WorkingDir: "{app}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional shortcuts:"

[Run]
Filename: "{app}\lh.exe"; Parameters: "init"; WorkingDir: "{app}"; Flags: runhidden waituntilterminated
Filename: "powershell.exe"; Parameters: "-ExecutionPolicy Bypass -File ""{app}\ConfigurationCenter.ps1"""; WorkingDir: "{app}"; Description: "Launch LuckyAgent Configuration Center"; Flags: postinstall nowait skipifsilent

[UninstallDelete]
Type: filesandordirs; Name: "{app}"

[Code]
const
  UserEnvironmentKey = 'Environment';

function PathContains(const Value, Entry: String): Boolean;
begin
  Result := Pos(';' + Lowercase(Entry) + ';', ';' + Lowercase(Value) + ';') > 0;
end;

procedure AddUserPath(const Entry: String);
var
  Existing, Updated: String;
begin
  if not RegQueryStringValue(HKCU, UserEnvironmentKey, 'Path', Existing) then
    Existing := '';
  if PathContains(Existing, Entry) then
    Exit;
  if Existing = '' then
    Updated := Entry
  else
    Updated := Existing + ';' + Entry;
  RegWriteExpandStringValue(HKCU, UserEnvironmentKey, 'Path', Updated);
end;

procedure RemoveUserPath(const Entry: String);
var
  Existing, Updated: String;
begin
  if not RegQueryStringValue(HKCU, UserEnvironmentKey, 'Path', Existing) then
    Exit;
  Updated := ';' + Existing + ';';
  StringChangeEx(Updated, ';' + Entry + ';', ';', True);
  while Pos(';;', Updated) > 0 do
    StringChangeEx(Updated, ';;', ';', True);
  if Updated = ';' then
    Updated := ''
  else
    Updated := Copy(Updated, 2, Length(Updated) - 2);
  RegWriteExpandStringValue(HKCU, UserEnvironmentKey, 'Path', Updated);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then begin
    AddUserPath(ExpandConstant('{app}'));
    AddUserPath(ExpandConstant('{app}\runtime\node'));
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then begin
    RemoveUserPath(ExpandConstant('{app}'));
    RemoveUserPath(ExpandConstant('{app}\runtime\node'));
  end;
end;
