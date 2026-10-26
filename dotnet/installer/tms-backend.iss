; Inno Setup script for TMS Backend (C#). Compiled by dotnet\build.ps1:
;   ISCC /DAppVersion=1.2.3 /DFileVersion=1.2.3 /DPublishDir=..\publish tms-backend.iss
;
; Installs tms-backend.exe as the Windows service "TMSBackend" (LocalSystem,
; delayed auto-start, restart on failure), writes .env from the wizard only
; when it does not exist yet, opens the HTTP port in the firewall and removes
; the old Go tray autostart. Uninstall removes the service and firewall rule but
; keeps .env, logs\ and archives\.
;
; Silent install for rollout (values are used only when .env does not exist):
;   tms-backend-setup.exe /VERYSILENT /PORT=8080 /DBHOST=10.0.0.5 /DBPORT=3306 /DBUSER=root
;     /DBPASSWORD=secret /DBNAME=tms_std /LEGACYURL=... /LEGACYTOKEN=... /MQTTBROKER=...
;     /MQTTTOPIC=... /MQTTCLIENTID=...

#ifndef AppVersion
  #define AppVersion "dev"
#endif
#ifndef FileVersion
  #define FileVersion "0.0.0"
#endif
#ifndef PublishDir
  #define PublishDir "..\publish"
#endif
#define ServiceName "TMSBackend"
#define FirewallRule "TMS Backend"

[Setup]
AppId={{E549F0A7-FD29-443D-AF0C-300E95A00FE8}
AppName=TMS Backend
AppVersion={#AppVersion}
AppVerName=TMS Backend {#AppVersion}
VersionInfoVersion={#FileVersion}
DefaultDirName=C:\TMS
DefaultGroupName=TMS Backend
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputBaseFilename=tms-backend-setup-{#AppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\tms-backend.exe
; Setup stops the service and old processes itself (PrepareToInstall).
CloseApplications=no
SetupLogging=yes

[Files]
Source: "{#PublishDir}\tms-backend.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "console-debug.cmd"; DestDir: "{app}"; Flags: ignoreversion

[Dirs]
Name: "{app}\logs"; Flags: uninsneveruninstall
Name: "{app}\archives"; Flags: uninsneveruninstall

[INI]
Filename: "{group}\Health check.url"; Section: "InternetShortcut"; Key: "URL"; String: "http://localhost:{code:GetPort}/health"

[Icons]
Name: "{group}\TMS Backend (console - debug)"; Filename: "{app}\console-debug.cmd"; WorkingDir: "{app}"; IconFilename: "{app}\tms-backend.exe"
Name: "{group}\Open logs folder"; Filename: "{app}\logs"
Name: "{group}\Edit config (.env)"; Filename: "{win}\notepad.exe"; Parameters: """{app}\.env"""
Name: "{group}\Uninstall TMS Backend"; Filename: "{uninstallexe}"

[UninstallDelete]
Type: files; Name: "{group}\Health check.url"

[Code]
var
  // Input query pages cannot scroll, so keep at most 3 fields per page
  // (more than that falls off the bottom of the wizard).
  ServerPage: TInputQueryWizardPage; // HTTP port, DB host, DB port
  DbPage: TInputQueryWizardPage;     // DB user, DB password, DB name
  LegacyPage: TInputQueryWizardPage; // Legacy API URL, token
  MqttPage: TInputQueryWizardPage;   // MQTT broker, topic, client ID

function EnvPath(): String;
begin
  Result := AddBackslash(WizardDirValue()) + '.env';
end;

function Run(const FileName, Params: String): Integer;
var
  Code: Integer;
begin
  if not Exec(FileName, Params, '', SW_HIDE, ewWaitUntilTerminated, Code) then
    Code := -1;
  Log(Format('%s %s -> %d', [FileName, Params, Code]));
  Result := Code;
end;

function Sc(const Params: String): Integer;
begin
  Result := Run(ExpandConstant('{sys}\sc.exe'), Params);
end;

function ServiceExists(): Boolean;
begin
  // 1060 = ERROR_SERVICE_DOES_NOT_EXIST
  Result := Sc('query {#ServiceName}') <> 1060;
end;

// net stop waits until the service has stopped (the app shuts down within 15s).
procedure StopService();
begin
  if ServiceExists() then
    Run(ExpandConstant('{sys}\net.exe'), 'stop {#ServiceName}');
end;

// Old Go tray builds, console debug runs: anything still holding the exe, port or COM ports.
procedure KillOldProcesses();
begin
  Run(ExpandConstant('{sys}\taskkill.exe'), '/F /IM tms-backend.exe');
  Sleep(1000);
end;

// The Go tray app registered itself in HKCU\...\Run (value "TMS-Backend") via a VBS launcher.
procedure RemoveGoTrayAutostart();
begin
  if RegDeleteValue(HKEY_CURRENT_USER, 'Software\Microsoft\Windows\CurrentVersion\Run', 'TMS-Backend') then
    Log('Removed Go tray autostart (HKCU Run\TMS-Backend)');
  DeleteFile(AddBackslash(WizardDirValue()) + 'tms-backend-startup.vbs');
  Run(ExpandConstant('{sys}\schtasks.exe'), '/Delete /TN "TMS-Backend" /F');
end;

function ReadEnvValue(const Key, Default: String): String;
var
  Lines: TArrayOfString;
  I: Integer;
  Line: String;
begin
  Result := Default;
  if not LoadStringsFromFile(EnvPath(), Lines) then
    Exit;
  for I := 0 to GetArrayLength(Lines) - 1 do
  begin
    Line := Trim(Lines[I]);
    if Pos(Key + '=', Line) = 1 then
    begin
      Result := Trim(Copy(Line, Length(Key) + 2, MaxInt));
      Result := RemoveQuotes(Result);
    end;
  end;
  if Result = '' then
    Result := Default;
end;

function GetPort(Param: String): String;
begin
  if FileExists(EnvPath()) then
    Result := ReadEnvValue('PORT', '8080')
  else
    Result := Trim(ServerPage.Values[0]);
  if Result = '' then
    Result := '8080';
end;

// Quote values that .env parsers would otherwise cut at '#' or whitespace.
function EnvValue(const S: String): String;
begin
  Result := S;
  if ((Pos('#', S) > 0) or (Pos(' ', S) > 0) or (Pos('"', S) > 0) or (Pos('\', S) > 0)) and (Pos('''', S) = 0) then
    Result := '''' + S + '''';
end;

procedure WriteEnvFile();
var
  S: String;
begin
  if FileExists(EnvPath()) then
  begin
    Log('.env already exists - keeping it');
    Exit;
  end;
  S :=
    '# TMS Backend config - created by Setup. Setup never overwrites this file;' + #13#10 +
    '# edit it and restart the service (net stop TMSBackend & net start TMSBackend).' + #13#10 +
    '' + #13#10 +
    'PORT=' + Trim(ServerPage.Values[0]) + #13#10 +
    '' + #13#10 +
    'DB_HOST=' + Trim(ServerPage.Values[1]) + #13#10 +
    'DB_PORT=' + Trim(ServerPage.Values[2]) + #13#10 +
    'DB_USER=' + EnvValue(Trim(DbPage.Values[0])) + #13#10 +
    'DB_PASSWORD=' + EnvValue(DbPage.Values[1]) + #13#10 +
    'DB_NAME=' + Trim(DbPage.Values[2]) + #13#10 +
    'DB_CHARSET=tis620' + #13#10 +
    '' + #13#10 +
    'POLL_INTERVAL=5m' + #13#10 +
    'ALERT_INTERVAL=5s' + #13#10 +
    '' + #13#10 +
    'LEGACY_API_URL=' + Trim(LegacyPage.Values[0]) + #13#10 +
    'LEGACY_API_TOKEN=' + EnvValue(Trim(LegacyPage.Values[1])) + #13#10 +
    '' + #13#10 +
    'MQTT_BROKER=' + Trim(MqttPage.Values[0]) + #13#10 +
    'MQTT_PORT=1883' + #13#10 +
    'MQTT_CLIENT_ID=' + Trim(MqttPage.Values[2]) + #13#10 +
    'MQTT_USERNAME=' + #13#10 +
    'MQTT_PASSWORD=' + #13#10 +
    'MQTT_TOPIC=' + Trim(MqttPage.Values[1]) + #13#10 +
    '' + #13#10 +
    '# ARCHIVE_DIR=./archives' + #13#10 +
    '# ARCHIVE_RETENTION_DAYS=30' + #13#10 +
    '# ARCHIVE_RUN_HOUR=2' + #13#10;
  if SaveStringToFile(EnvPath(), S, False) then
    Log('.env written')
  else
    Log('ERROR: could not write .env');
end;

procedure InstallService();
var
  Exe, BinPath: String;
begin
  Exe := ExpandConstant('{app}\tms-backend.exe');
  BinPath := 'binPath= "\"' + Exe + '\"" start= delayed-auto DisplayName= "TMS Backend"';
  // An existing TMSBackend service (an earlier install, or an NSSM wrapper around
  // the Go exe) is reconfigured in place: sc delete can stay pending for a while.
  if ServiceExists() then
    Sc('config {#ServiceName} ' + BinPath)
  else
    Sc('create {#ServiceName} ' + BinPath);
  Sc('description {#ServiceName} "TMS temperature monitoring backend (REST API, device polling, alerts)"');
  Sc('failure {#ServiceName} reset= 86400 actions= restart/5000/restart/10000/restart/30000');
  // Also run the recovery actions when the process exits with an error code, not only on crashes.
  Sc('failureflag {#ServiceName} 1');
end;

procedure OpenFirewall();
var
  Netsh: String;
begin
  Netsh := ExpandConstant('{sys}\netsh.exe');
  Run(Netsh, 'advfirewall firewall delete rule name="{#FirewallRule}"');
  Run(Netsh, 'advfirewall firewall add rule name="{#FirewallRule}" dir=in action=allow protocol=TCP localport=' + GetPort(''));
end;

procedure InitializeWizard();
begin
  ServerPage := CreateInputQueryPage(wpSelectDir, 'Server and database (1/2)',
    'Written to .env only for a new install - an existing .env is kept and these pages are skipped',
    '');
  ServerPage.Add('HTTP port:', False);
  ServerPage.Add('DB host:', False);
  ServerPage.Add('DB port:', False);
  ServerPage.Values[0] := ExpandConstant('{param:PORT|8080}');
  ServerPage.Values[1] := ExpandConstant('{param:DBHOST|}');
  ServerPage.Values[2] := ExpandConstant('{param:DBPORT|3306}');

  DbPage := CreateInputQueryPage(ServerPage.ID, 'Server and database (2/2)',
    'Database login',
    '');
  DbPage.Add('DB user:', False);
  DbPage.Add('DB password:', True);
  DbPage.Add('DB name:', False);
  DbPage.Values[0] := ExpandConstant('{param:DBUSER|root}');
  DbPage.Values[1] := ExpandConstant('{param:DBPASSWORD|}');
  DbPage.Values[2] := ExpandConstant('{param:DBNAME|tms_std}');

  LegacyPage := CreateInputQueryPage(DbPage.ID, 'Legacy API',
    'Optional - leave empty to turn it off (can be changed later in .env)',
    '');
  LegacyPage.Add('Legacy API URL:', False);
  LegacyPage.Add('Legacy API token:', False);
  LegacyPage.Values[0] := ExpandConstant('{param:LEGACYURL|}');
  LegacyPage.Values[1] := ExpandConstant('{param:LEGACYTOKEN|}');

  MqttPage := CreateInputQueryPage(LegacyPage.ID, 'MQTT',
    'Optional - leave the broker empty to turn MQTT off (can be changed later in .env)',
    '');
  MqttPage.Add('MQTT broker (host):', False);
  MqttPage.Add('MQTT topic:', False);
  MqttPage.Add('MQTT client ID:', False);
  MqttPage.Values[0] := ExpandConstant('{param:MQTTBROKER|}');
  MqttPage.Values[1] := ExpandConstant('{param:MQTTTOPIC|tms/temperature}');
  MqttPage.Values[2] := ExpandConstant('{param:MQTTCLIENTID|}');
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := ((PageID = ServerPage.ID) or (PageID = DbPage.ID) or (PageID = LegacyPage.ID) or (PageID = MqttPage.ID))
    and FileExists(EnvPath());
end;

function IsNumber(const S: String): Boolean;
begin
  Result := (S <> '') and (StrToIntDef(S, -1) > 0) and (StrToIntDef(S, -1) < 65536);
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if CurPageID = ServerPage.ID then
  begin
    if (not IsNumber(Trim(ServerPage.Values[0]))) or (not IsNumber(Trim(ServerPage.Values[2]))) then
    begin
      SuppressibleMsgBox('HTTP port and DB port must be numbers between 1 and 65535.', mbError, MB_OK, IDOK);
      Result := False;
    end
    else if Trim(ServerPage.Values[1]) = '' then
    begin
      SuppressibleMsgBox('DB host is required.', mbError, MB_OK, IDOK);
      Result := False;
    end;
  end
  else if CurPageID = DbPage.ID then
  begin
    if Trim(DbPage.Values[2]) = '' then
    begin
      SuppressibleMsgBox('DB name is required.', mbError, MB_OK, IDOK);
      Result := False;
    end;
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  // Free the exe (upgrade), the HTTP port and the COM ports before copying files.
  StopService();
  KillOldProcesses();
  RemoveGoTrayAutostart();
  Result := '';
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    WriteEnvFile();
    InstallService();
    OpenFirewall();
    if Run(ExpandConstant('{sys}\net.exe'), 'start {#ServiceName}') <> 0 then
      SuppressibleMsgBox('The TMSBackend service was installed but did not start.' + #13#10 +
        'Check .env and the logs in ' + ExpandConstant('{app}\logs') + ', the Windows Event Log (Application),' + #13#10 +
        'or run "TMS Backend (console - debug)" from the Start Menu.', mbError, MB_OK, IDOK);
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
  begin
    if Sc('query {#ServiceName}') <> 1060 then
    begin
      Run(ExpandConstant('{sys}\net.exe'), 'stop {#ServiceName}');
      Sc('delete {#ServiceName}');
    end;
    Run(ExpandConstant('{sys}\netsh.exe'), 'advfirewall firewall delete rule name="{#FirewallRule}"');
    // .env, logs\ and archives\ are left in place on purpose.
  end;
end;
