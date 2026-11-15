; Установщик Филина (Inno Setup). Собирается release.ps1:
;   ISCC.exe /DAppVersion=1.3.0 /DExe=..\dist\filin.exe /O..\dist installer\filin.iss
;
; Журнал установки — %TEMP%\Setup Log <дата> #NNN.txt (SetupLogging): по нему разбирать жалобы.
; Ставит для текущего пользователя, без прав администратора: Филин сам обновляет свой exe,
; а в Program Files без прав администратора не записать. Папка по умолчанию — папка
; пользователя, не «Документы»: их часто синхронизирует OneDrive.
; Библиотека живёт в той же папке (подпапка «библиотека») — удаление спрашивает о ней отдельно.

#ifndef AppVersion
  #error Задайте версию: /DAppVersion=1.3.0
#endif
#ifndef Exe
  #define Exe "..\dist\filin.exe"
#endif

[Setup]
; AppId не менять никогда: по нему Windows узнаёт, что это тот же Филин (обновление, удаление)
AppId={{64EB7223-A064-41F8-AEAE-9C67B7AFF02E}
AppName=Филин
AppVersion={#AppVersion}
AppVerName=Филин {#AppVersion}
AppPublisher=Филин
AppPublisherURL=https://github.com/aleksandrgradoboev-svg/Filin
AppSupportURL=https://github.com/aleksandrgradoboev-svg/Filin/issues
AppUpdatesURL=https://github.com/aleksandrgradoboev-svg/Filin/releases
DefaultDirName={%USERPROFILE}\Филин
SetupLogging=yes
PrivilegesRequired=lowest
DisableProgramGroupPage=yes
DisableWelcomePage=yes
DisableDirPage=no
UsePreviousAppDir=yes
WizardStyle=modern
WizardImageFile=wizard.png
WizardSmallImageFile=wizard-small.png
SetupIconFile=..\internal\desktop\filin.ico
UninstallDisplayIcon={app}\filin.ico
UninstallDisplayName=Филин
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
; модели распознавания Филин докачает сам — место под них нужно сразу
ExtraDiskSpaceRequired=45000000
OutputBaseFilename=filin-setup
Compression=lzma2/max
SolidCompression=yes
; запущенный Филин перед заменой закрыть: Windows сама предложит
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "ru"; MessagesFile: "compiler:Languages\Russian.isl"

[Messages]
ru.SelectDirLabel3=Программа установит [name] в следующую папку. В ней же будет храниться ваша библиотека — в подпапке «библиотека».

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "{#Exe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\internal\desktop\filin.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
; те же пути и имя, что у ярлыков, которые делает сам Филин (filin install): при первом
; запуске он увидит, что ярлыки уже есть, и не станет про них спрашивать
Name: "{userprograms}\Филин"; Filename: "{app}\filin.exe"; Parameters: "app"; WorkingDir: "{app}"; IconFilename: "{app}\filin.ico"; Comment: "Филин — каталог домашней библиотеки"
Name: "{userdesktop}\Филин"; Filename: "{app}\filin.exe"; Parameters: "app"; WorkingDir: "{app}"; IconFilename: "{app}\filin.ico"; Comment: "Филин — каталог домашней библиотеки"; Tasks: desktopicon

[Run]
Filename: "{app}\filin.exe"; Parameters: "app"; WorkingDir: "{app}"; Description: "{cm:LaunchProgram,Филин}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; работающий Филин держит базу и журнал — перед удалением закрыть
Filename: "{sys}\taskkill.exe"; Parameters: "/IM filin.exe /F"; Flags: runhidden; RunOnceId: "CloseFilin"

[UninstallDelete]
; хозяйство программы — удаляется всегда; библиотека и копии — только по ответу «Да» (ниже)
Type: filesandordirs; Name: "{app}\модели"
Type: filesandordirs; Name: "{app}\телефон"
Type: filesandordirs; Name: "{app}\журнал"
Type: files; Name: "{app}\filin.exe.old"
Type: files; Name: "{app}\filin.exe.new"
Type: files; Name: "{app}\filin.exe.*.part"
Type: files; Name: "{app}\.ярлыки-спрошены"

[Code]
// CanWriteTo — можно ли этому пользователю создать папку Dir: пробуем создать и сразу убрать
// временную папку в ближайшей существующей. Без этой проверки выбранная через «Обзор» чужая
// папка (старый профиль другой учётной записи, 15.11.2026) роняла установку уже после
// «Установить» — «Ошибка 5: Отказано в доступе».
function CanWriteTo(Dir: String): Boolean;
var
  P, Probe: String;
begin
  P := RemoveBackslashUnlessRoot(Dir);
  while (not DirExists(P)) and (ExtractFileDir(P) <> P) do
    P := ExtractFileDir(P);
  Probe := AddBackslash(P) + '.filin-проба-записи-' + IntToStr(Random(1000000));
  Result := CreateDir(Probe);
  if Result then
    RemoveDir(Probe);
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if (CurPageID = wpSelectDir) and (not CanWriteTo(WizardDirValue)) then
  begin
    Log('нет записи в ' + WizardDirValue);
    MsgBox('В папку' + #13#10 + WizardDirValue + #13#10 +
           'Windows не даёт записывать: она принадлежит другой учётной записи или системе.' + #13#10#13#10 +
           'Выберите другую — например, ту, что предложена по умолчанию.', mbError, MB_OK);
    Result := False;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Lib, Copies: String;
begin
  if CurUninstallStep <> usPostUninstall then
    Exit;
  Lib := ExpandConstant('{app}\библиотека');
  Copies := ExpandConstant('{app}\копии');
  // тихое удаление библиотеку не трогает никогда; вопрос — по умолчанию «Нет»
  if (not UninstallSilent) and (DirExists(Lib) or DirExists(Copies)) then
    if MsgBox('Удалить вместе с программой и вашу библиотеку?' + #13#10#13#10 +
              'Книги, обложки и копии лежат в папках' + #13#10 + Lib + #13#10 + Copies + #13#10#13#10 +
              '«Нет» — программа удалится, библиотека останется на месте.',
              mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
    begin
      DelTree(Lib, True, True, True);
      DelTree(Copies, True, True, True);
    end;
  RemoveDir(ExpandConstant('{app}')); // только если пуста: с библиотекой папка остаётся
end;
