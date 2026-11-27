# RTX UV Cyberpunk

Eksperymentalne narzędzie dla **Windows 11 x64 i NVIDIA GeForce RTX 4070 SUPER 12 GB**. Szuka krzywej napięcie–częstotliwość (V/F), która przy podobnej wydajności w benchmarku Cyberpunk 2077 pozwala karcie pracować przy niższym napięciu. Punktem odniesienia jest profil OC Scanner już zapisany w MSI Afterburner.

**Status wydania.** Logikę sprawdzono w testach programowych i symulacji. Zapis do rzeczywistego sterownika NVIDIA, uruchamianie benchmarku z konkretną wersją gry, import slotu MSI i odzyskiwanie po resecie GPU nadal wymagają weryfikacji sprzętowej. Zacznij od **Dry Run**. Ten tryb uruchamia grę, odczytuje telemetrię i planuje testy bez zapisu krzywej GPU.

## Pobranie i uruchomienie

1. Pobierz `RTX-UV-Cyberpunk-Windows11.zip` z [Releases](../../releases) i rozpakuj cały plik. Paczka zawiera niepodpisany EXE, źródła, instrukcję i sumę SHA-256.
2. Uruchom Cyberpunk 2077 przynajmniej raz, zamknij grę i przygotuj MSI Afterburner z istniejącym profilem OC Scanner. Zastosuj ten profil w MSI.
3. Otwórz `RTX-UV-Cyberpunk.exe`. Wskaż plik `Cyberpunk2077.exe` z `bin\x64`, plik `MSIAfterburner.exe` i numer oryginalnego slotu MSI.
4. Wybierz **Dry Run**. Jeśli odczyty i eksporty gry nie pasują do oczekiwanego formatu, program przerwie sesję i zgłosi przyczynę.

## Tryby

| Tryb | Zachowanie |
| --- | --- |
| **Zmierz obecny OC** | Rozgrzewka i trzy benchmarki na obecnej krzywej. |
| **Dry Run** | Jeden benchmark na obecnej krzywej i symulacja kolejnych napięć. |
| **Rozpocznij optymalizację** | Po potwierdzeniu sprawdza odtworzenie oryginału i tymczasowo testuje kolejne krzywe. Kończy przywróceniem oryginalnego profilu i pomiarem kontrolnym. |
| **Wypróbuj UV przez 5 min** | Po potwierdzeniu tymczasowo stosuje krzywą zwycięzcy z ukończonej sesji. |
| **Zapisz UV w MSI…** | Po zakończeniu gry i MSI dodaje zweryfikowaną krzywą zwycięzcy do potwierdzonego wolnego slotu. Przed zapisem robi kopię pliku. Sam import nie aktywuje UV i nie zmienia sekcji `Startup`. |

## Dobór krzywej i ocena

Program odnosi napięcia do zmierzonego obciążenia. Zaczyna od kroku około 25 mV, a przy pierwszej regresji zawęża przedział z krokiem około 12,5 mV i sąsiednimi punktami udostępnionymi przez kartę. Każdy przebieg zostawia osobny eksport i próbki telemetrii. Ocena sprawdza między innymi średnie FPS, 1% Low, 0,1% Low, P99 frametime, zegar, napięcie, pobór mocy i temperaturę. Finalista musi zaliczyć pięć powtórek oraz co najmniej 60 minut sumarycznego czasu powtarzanej sceny przed potwierdzeniem wyniku. Remis pozostawia pierwotny OC.

## Odzyskiwanie

Eksperyment korzysta z tabeli V/F rdzenia sterownika. Wymaga kopii oryginalnej krzywej i konfiguracji MSI oraz działającego procesu nadzorującego. Ścieżka zmiany krzywej nie modyfikuje VBIOS-u, rejestru TDR ani ustawień VRAM, limitów i wentylatorów. Przycisk **STOP**, błąd procesu gry lub wykryty reset sterownika kończą test i uruchamiają próbę przywrócenia. Po restarcie systemu pomocnik może tylko przywrócić oryginał. Wymaga to zalogowania na to samo konto i dostępu do kopii. Przy niepotwierdzonym odzyskaniu następna optymalizacja jest zablokowana.

Program odczytuje telemetrię oraz zdarzenia Windows. Artefakty widoczne w grze mogą pojawić się bez błędu sterownika i bez sygnału w telemetrii. W takim przypadku przerwij próbę przyciskiem **STOP**. W pełni zawieszony system może wymagać ponownego uruchomienia.

## Kompilacja i testy

Na Windows z Go 1.24 lub nowszym uruchom `Kompiluj.cmd`. Skrypt tworzy binarium Windows x64. Dane stanowiska testowego oraz szczegóły kryteriów i zgodności są w [pełnej instrukcji](Instrukcja.md).

```sh
cd source
go test ./...
```

Na Linuksie z Go i kompilatorem C można uruchomić symulację z katalogu głównego repozytorium.

```sh
bash testy-symulacyjne/test.sh
```

MSI Afterburner, sterownik NVIDIA i Cyberpunk 2077 są wymaganiami zewnętrznymi i korzystasz z własnych instalacji. Zgłaszając problem, podaj wersję Windows, sterownika, MSI i gry. Pliki sesji mogą zawierać lokalne ścieżki i konfigurację MSI. Do publicznego zgłoszenia wystarczy komunikat błędu i opis etapu.
