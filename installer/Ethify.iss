#define AppName "Ethify"
#ifndef AppVersion
  #define AppVersion "1.1.1"
#endif
#ifndef StageDir
  #define StageDir "..\dist\win-stage"
#endif

[Setup]
AppId={{93C2A0A7-6E1B-4A4D-8DAE-3F111CC19B3C}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher=A2L
DefaultDirName={autopf}\Ethify
DefaultGroupName=Ethify
UninstallDisplayIcon={app}\Ethify.exe
OutputDir=..\dist\installers
OutputBaseFilename=Ethify-{#AppVersion}-windows-x64-setup
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
PrivilegesRequired=admin
Compression=lzma2
SolidCompression=yes
WizardStyle=modern

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional shortcuts:"

[Files]
Source: "{#StageDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\Ethify"; Filename: "{app}\Ethify.exe"; WorkingDir: "{app}"
Name: "{autodesktop}\Ethify"; Filename: "{app}\Ethify.exe"; WorkingDir: "{app}"; Tasks: desktopicon

[Run]
Filename: "{app}\Ethify.exe"; Description: "Launch Ethify"; Flags: postinstall nowait skipifsilent
