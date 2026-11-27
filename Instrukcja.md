# RTX UV · Cyberpunk 2077

Przenośna aplikacja dla Windows 11 x64 i jednej karty NVIDIA GeForce RTX 4070 SUPER 12 GB. Do uruchomienia EXE potrzebujesz zainstalowanego sterownika NVIDIA, MSI Afterburner i własnej instalacji Cyberpunk 2077.

## Wynik ponownego audytu

Na stanowisku testowym dostępny był Linux x64. Odtworzono przejście przez logikę pomiaru OC, Dry Run, wyszukiwania, pięciu powtórzeń i sumowania czasu sceny. Test symulacyjny obejmuje także STOP w podglądzie oraz błąd sterownika w finale, blokadę odzyskiwania na niezgodnym sterowniku i weryfikację przywrócenia po powrocie zgodnego odczytu. W symulacji wyniki gry, procesy Windows, MSI i sterownik zastępują dane testowe. Ten kod znajduje się poza katalogiem kompilacji EXE. Testy możesz powtórzyć na Linuksie z Go i kompilatorem C poleceniem `bash testy-symulacyjne/test.sh`. W katalogu `source` polecenie `go test -race ./...` sprawdza oddzielne przypadki regresji.

W nowej sesji odzyskiwanie odczytuje numer slotu i tryb z kopii oryginału. Sprawdza SHA-256 każdego pliku MSI w archiwum. Uszkodzony zapis wyników benchmarków nie uniemożliwia mu odczytu tych danych. Jeśli masz oczekujące odzyskiwanie ze starszej sesji, dokończ je przed uruchomieniem nowej. Podgląd UV wymaga sesji utworzonej tą wersją, pięciu zaliczonych powtórzeń, pełnych 3600 sekund zaliczonych scen i końcowej kontroli bazowego OC.

**Ocena dla Twojej karty.** Brakuje próby na Windows 11 z Twoją RTX 4070 SUPER, wersją MSI, sterownikiem i grą. Na tym etapie zalecany jest **Dry Run** lub **Zmierz obecny OC**. Odtworzenie przez rzeczywisty sterownik, zatrzymanie zawieszonej gry i odzyskiwanie po logowaniu pozostają niepotwierdzone. Czas wykonania STOP zależy także od odpowiedzi sterownika. Watchdog nadzoruje proces, ale nie może wykonać odtworzenia GPU podczas zawieszenia całego systemu. Odzyskanie po restarcie wymaga zalogowania na to samo konto i dostępności plików kopii.

## Zapis zwycięskiej krzywej do osobnego slotu MSI

Po pomyślnej optymalizacji i końcowej weryfikacji oryginalnego OC odblokuje się przycisk **Zapisz UV w MSI…**. Możesz też wybrać ukończoną sesję w historii. Pierwszy benchmark i Dry Run nie tworzą profilu do importu.

1. Po zakończeniu sesji zamknij Cyberpunka i zakończ MSI Afterburner również w zasobniku systemowym.
2. Kliknij **Zapisz UV w MSI…**. Program proponuje najbliższy wolny slot w kolejności rosnącej, po slocie oryginalnym. Dla oryginalnego slotu 2 najpierw sprawdzi 3. Jeśli wszystkie pozostałe sloty są zajęte, import zostanie przerwany.
3. Potwierdź wyświetlony numer. Program porówna oryginalny slot i sekcję `Startup` z kopią sesji. Zapisze osobną kopię aktualnego pliku profili, doda zwycięską krzywą w wolnej sekcji i odczyta zapisany plik. Zachowuje oryginalny slot, inne zapisane profile i ustawienia startowe. Kopię pliku sprzed importu znajdziesz w folderze sesji pod nazwą `MSI-przed-importem-*.cfg`.
4. Uruchom MSI Afterburner. Nowy slot możesz wybrać ręcznie. Sam import nie stosuje UV i nie zmienia autostartu. Po imporcie podgląd ze starej sesji może zgłosić, że konfiguracja MSI różni się od kopii, ponieważ został dodany profil.

Import obsługuje pliki profili MSI w formacie 2 z jednoznacznym dopasowaniem wszystkich punktów V/F. Funkcja zapisuje plik konfiguracji, nie wywołuje NVAPI ani MSI. Testy na danych przykładowych sprawdzają dodawanie do pustego slotu, zachowanie bajtów istniejących sekcji i zapis offsetów krzywej. Kompilacja obejmuje także test zapisu pliku na Windowsie. Uruchomienie tego testu, rozpoznanie profilu w Twojej wersji MSI oraz odczyt punktów z karty po ręcznym zastosowaniu pozostają do potwierdzenia na Windows 11.

## Pierwsze uruchomienie

1. Rozpakuj cały ZIP do zwykłego folderu. Uruchom wcześniej Cyberpunka co najmniej raz, pozostaw swoje ustawienia grafiki i zamknij grę.
2. W MSI Afterburner wczytaj i zastosuj zapisany **oryginalny profil OC Scanner**, którego używasz. Zapamiętaj numer slotu 1–5. Jeżeli oryginalna krzywa jest zapisana wyłącznie jako bieżący stan, zapisz ją w potwierdzonym wolnym slocie w samym MSI. Pozostaw MSI uruchomiony, zwłaszcza gdy steruje krzywą wentylatorów.
3. Uruchom `RTX-UV-Cyberpunk.exe`. Wybierz `Cyberpunk2077.exe` z folderu gry `bin\x64` i plik `MSIAfterburner.exe`. Podaj numer zapisanego slotu OC. Jeżeli wydanie sklepu wymaga klienta, pozostaw go uruchomionego. Zamknij grę oraz zakończ automatyczne strojenie GPU w innych programach.
4. Uruchom **Dry Run**, aby sprawdzić przepływ pomiarów bez zapisu do GPU. Program wykona jeden benchmark przy oryginalnym OC, bez rozgrzewki i serii pomiarowej. Sprawdzi też zapisany slot MSI, uruchomi watchdoga i przejdzie przez ten sam planer co optymalizacja. Plik `plan-dry-run.json` zawiera przewidywane punkty i symulowane rozstrzygnięcia. W działającej instalacji wyniki FPS pochodzą z eksportów gry na oryginalnym OC. Okno telemetrii jest przybliżane czasem zapisu eksportu i długością sceny, z odrzuceniem przerw między próbkami przekraczających 2,5 s. **Zmierz obecny OC** wykona rozgrzewkę i trzy pomiary bazowe. Oba tryby zapisują kopię MSI i zrzut V/F.
5. Kliknij **Rozpocznij optymalizację**, aby przejść przez rozgrzewkę, trzy nowe pomiary OC, próbę odtwarzania i kolejne napięcia. Pozostaw grę na pierwszym planie podczas sceny. Czas zależy od liczby profili, których wyniki uzasadniają test godzinny. Na każdy taki profil przypada co najmniej 60 minut samej sceny, bez doliczania uruchamiania gry, chłodzenia i przebiegów kontrolnych.

Aplikacja odczytuje istniejący plik ustawień gry oraz tworzy kopię konfiguracji instalacji MSI i całego katalogu `Profiles` w ZIP-ie sesji. Zrzut karty zawiera odczytane punkty krzywej, ich indywidualne offsety, maskę, surowe struktury NVAPI, identyfikator GPU, ustawienia P-state, limity mocy i temperatury oraz dostępne dane wentylatorów. Znak „—” dla czujnika oznacza brak odczytu. Tryb optymalizacji wymaga również odczytu Hot Spot, PerfCap i zegara VRAM. W trybach odczytowych część czujników może być niedostępna. Parametry RGB, rozdzielczość, DLSS i Frame Generation pozostają takie jak w ustawieniach gry.

### Błąd odczytu punktu NVAPI

Przy nieprawidłowym odczycie krzywej aplikacja powtarza odczyt i zapisuje w komunikacie wersję sterownika. Komunikat o błędnym aktywnym punkcie zawiera jego numer, napięcie w µV, zegar i offset w kHz. Puste wpisy końcowe są pomijane, gdy cały rekord stanu oraz offset są zerowe i za nimi brak kolejnych aktywnych punktów rdzenia. Niejednoznaczny odczyt blokuje dobór UV. Jeśli błąd powtarza się w **Dry Run**, do diagnozy wystarczy pełna treść komunikatu.

Sterownik 621.40 na RTX 4070 SUPER zgłosił w końcowej części tabeli wpis o zegarze 10 451 MHz i offsecie 200 MHz. Aplikacja oddziela wpisy o charakterystyce pamięci GDDR6X dopiero za kompletnym zakresem odczytu rdzenia i wyłącza ich indeksy z maski zapisu. Przed strojeniem nadal wymaga dopasowania całej odczytanej krzywej rdzenia do wskazanego slotu MSI. Jeśli po granicy wystąpi kolejny punkt rdzenia, aplikacja przerwie sesję.

### Benchmark z menu gry

Jeżeli automatyczny start zaczyna scenę przed pełnym załadowaniem renderowania, zaznacz **Uruchamiaj benchmark z menu gry**. Dla każdej próby aplikacja otworzy Cyberpunka do menu. Poczekaj na pełne załadowanie, sprawdź stan Path Tracingu i wybierz **Ustawienia → Grafika → Uruchom benchmark**. Po wyświetleniu wyniku zamknij grę. Program odczyta nowy eksport i sam przejdzie do następnej próby. Na pojedynczą próbę wraz z ładowaniem i zamknięciem gry są 4 minuty.

Ustawienia grafiki zapisz przed sesją. Aplikacja przerywa pomiar przy zmianie pliku ustawień. Jeśli zobaczysz zmianę renderowania w trakcie sceny, przerwij próbę i rozpocznij nową sesję od początku. Aplikacja czyta plik ustawień i eksporty benchmarku, a stan renderowania podczas sceny wymaga obserwacji na komputerze.

## Granice zmian w GPU

Aplikacja zmienia wyłącznie wybrane offsety częstotliwości rdzenia na istniejących punktach V/F przez sterownik NVIDIA. Wyższe taktowanie przy niższym napięciu może odpowiadać częstotliwości, którą fabryczna siatka z oryginalną krzywą OC Scanner udostępniała już przy wyższym napięciu. Siatka napięć, VBIOS, firmware, zabezpieczenia GPU i fabryczne limity pozostają nietknięte. Aplikacja odczytuje zdarzenia TDR z dziennika Windows. Parametry `TdrDelay` i `TdrLevel` pozostają takie jak przed uruchomieniem.

Próby mają dodatkowy próg przerwania równy najwyższej próbce napięcia z trzech bazowych benchmarków OC. Program sprawdza napięcie przed i po zmianie krzywej oraz podczas oczekiwania i pomiaru. Odczyt powyżej progu lub utrata telemetrii kończą sesję i uruchamiają odzyskiwanie. Telemetria sprawdza dostępne próbki. Rozdzielczość czasowa pomiaru podczas sceny wynosi 0,5 s. Zapisy eksperymentalne i pomiary są wstrzymywane przy odczycie GPU od 87°C lub Hot Spot od 105°C.

Przed każdym zapisem eksperymentalnym aplikacja sprawdza, czy niezależny watchdog nadal działa, czy jego ostatni sygnał ma najwyżej 12 sekund oraz czy dziennik, kopia oryginalnego profilu i wpis odzyskiwania po zalogowaniu są zgodne z zapisem. Utrata nadzoru kończy próbę. Przy odtwarzaniu proces odczytuje osobną kopię i potwierdza powiązanie dziennika z oryginalnymi offsetami, trybem sesji i numerem slotu MSI. Bez zgodnej kopii nie wysyła niezweryfikowanej krzywej do sterownika.

## Jak aplikacja ocenia profil

- Domyślnie uruchamia grę z argumentem `-benchmark`. Opcjonalnie otwiera menu gry i czeka na ręczne uruchomienie sceny oraz zamknięcie gry po wyniku. Wymaga nowych plików `frames.csv` oraz `summary.json`. Sprawdza obciążenie GPU w oknie sceny wyznaczonym z eksportu. Zmiana odcisku `UserSettings.json` przerywa sesję.
- Porównuje liczbę klatek, czas i FPS obu eksportów. Dopuszcza proporcję 2:1 między podsumowaniem i CSV, spotykaną przy generowaniu klatek, jeśli potwierdzają ją także czasy oraz FPS. Przez całą sesję porównuje przebiegi z tą samą proporcją. Pokazuje osobno **FPS CSV** i **FPS gry**.
- Na podstawie trzech benchmarków wyznacza rozrzut FPS, 1% Low, 0,1% Low, frametime P99 i zegara GPU. Zbyt duży rozrzut bazowy przerywa optymalizację. Prędkość i wskaźniki płynności używane do oceny profili są liczone z czasów klatek zapisanych w CSV. 1% Low i 0,1% Low to odwrotność średniego czasu odpowiednio 1% i 0,1% najwolniejszych klatek z tego pliku.
- Wybiera taktowanie blisko krzywej w punkcie rzeczywistego obciążenia i przesuwa punkt docelowy na kolejne niższe napięcia dostępne na karcie. Indywidualne offsety pod kotwicą zostają zachowane. Wyższe punkty są ograniczane do taktowania kotwicy. Kombinacje poza zakresem sterownika lub powyżej maksymalnego zegara pierwotnej krzywej są odrzucane.
- Pierwszym celem jest napięcie o 25 mV niższe od zmierzonego OC. Program wybiera najbliższy niższy punkt siatki karty. Przy granicy przechodzi na 12,5 mV, zawęża przedział i sprawdza sąsiednie punkty. Program przewiduje po pięć nowych benchmarków dla maksymalnie trzech finalistów. Pierwszy wynik poniżej kryteriów kończy serię tego finalisty i wyzwala powrót do OC. Do pozytywnej kwalifikacji wszystkie pięć musi osobno przejść kryteria wydajności, zegara i napięcia. Każdy finalista z przewagą nad OC przechodzi także co najmniej 60 minut obciążenia sceną. Porównanie sprawdza kolejno średni FPS, 1% Low, wahania zegara, pobór mocy i temperaturę. 0,1% Low, P99 i rzeczywisty zegar GPU służą do odrzucania regresji. Remis oznacza zachowanie oryginalnego OC.
- Przed pomiarem CPU i GPU muszą mieć co najwyżej 12% obciążenia przez pięć kolejnych odczytów. W trybach pomiaru OC i optymalizacji pierwszy benchmark to rozgrzewka poza oceną. Po nim aplikacja czeka co najmniej 45 sekund i ustala cel temperatury ze stabilnych odczytów w bezczynności. Kolejne próby muszą się mieścić w przedziale ±2°C względem tego celu. Program wykrywa wybrane konkurencyjne aplikacje do strojenia i obciążania GPU przed pomiarem i okresowo podczas niego. Kontroluje działanie MSI Afterburner, zgodność aktywnego profilu i odcisk ustawień gry. Czeka na odpowiednie warunki maksymalnie siedem minut. Pomiar OC wraca okresowo podczas doboru, między finalistami i w przybliżeniu co dziesięć minut sceny testu godzinnego.
- Pętla godzinowa składa się z powtarzanej sceny gry. Nie obejmuje ciągłego obciążenia bez przerw ani innych typów obciążenia GPU. Jej zaliczenie opisuje zachowanie w tym teście, z danym sterownikiem i ustawieniami gry.
- Sterownik nie gwarantuje, że GPU użyje dokładnie napięcia kotwicy. Aplikacja porównuje je z rzeczywistą medianą z obciążenia. Profil może przegrać mimo niższej kotwicy. Gdy żaden UV nie przejdzie kryteriów, wynik stanowi oryginalny OC.
- Na wykresie przełączysz telemetrię lub przebiegi FPS i frametime. W folderze sesji znajdują się także surowe eksporty gry, próbki GPU co 0,5 s, log procesu, zdarzenia NVIDIA znalezione w dzienniku Windows i zapis postępu po każdej próbie.

## Przywracanie i wybór UV

1. Przed pierwszym pomiarem w trybie optymalizacji aplikacja uruchamia niezależny proces odzyskiwania i zapisuje kopię oryginalnych ustawień. Przed właściwym UV obniża wybrane punkty krzywej na krótko maksymalnie o 30 MHz, sprawdza rezultat, a następnie odtwarza oryginalne offsety i weryfikuje wynik. Jeszcze przed pierwszym eksperymentalnym zapisem wymaga odwzorowania wszystkich odczytanych punktów rdzenia w slocie MSI, wczytuje oryginalny slot i sprawdza jego odczyt. Offsety zapisu są porównywane dokładnie. Dla częstotliwości raportowanych na żywo porównanie po przywróceniu dopuszcza wspólną zmianę do 45 MHz i odchylenie pojedynczego punktu do 15 MHz względem tej zmiany. Powtarza próbę odtworzenia z oryginalnego slotu MSI. Jeżeli którykolwiek test się nie powiedzie, sesja kończy się przed właściwym undervoltingiem.
2. W trakcie sesji program kontroluje tablicę V/F, limity, offset VRAM i pliki MSI. Słabszy wynik z poprawnie ukończonego benchmarku powoduje powrót do oryginalnego OC przed oceną kolejnej opcji. Zawieszenie lub awaria gry, TDR, utrata dostępu do GPU i naruszenie progu napięcia kończą całą sesję i wyzwalają odtworzenie oryginalnego OC. **STOP** anuluje próbę, kończy proces gry rozpoczęty przez aplikację i uruchamia odtworzenie oryginału. W trakcie benchmarku program odczytuje telemetrię oraz dziennik systemowy. Niedostępny dziennik lub nieprawidłowe dane mogą przerwać sesję.
3. Gdy aplikacja lub cały komputer przestaną odpowiadać, kopia procesu odzyskiwania korzysta z zapisanego stanu. Przy restarcie uruchamia się po zalogowaniu na to samo konto, dzięki własnemu, tymczasowemu plikowi w folderze Autostart Windows. Plik jest usuwany po potwierdzeniu odzyskania. Proces po restarcie obsługuje wyłącznie oryginalny zapisany profil. Nową sesję można uruchomić ręcznie po weryfikacji. Autostart MSI, rejestr Windows, VBIOS oraz trwałe ustawienia sterownika pozostają bez zmian.
4. Jeżeli po awarii widzisz komunikat o oczekującym odzyskaniu, wczytaj zapisany oryginalny slot z MSI i kliknij w aplikacji **Odzyskaj oryginał**. Weryfikacja sprawdza offset każdego punktu, kształt krzywej, chronione parametry i zgodność plików MSI z kopią. Przy niezgodnym odczycie wpis odzyskiwania pozostaje aktywny, a aplikacja blokuje strojenie. Po zmianie sterownika lub karty automat wstrzymuje zapis do czasu zgodnego odczytu na konfiguracji z kopii.
5. Na końcu aplikacja wczytuje oryginalny slot MSI, porównuje go z kopią i wykonuje kontrolny benchmark. Poprawny wynik odblokowuje przyciski decyzji. **Wypróbuj UV przez 5 min** stosuje zwycięską krzywą dopiero po potwierdzeniu. Kliknięcie **STOP** lub upływ pięciu minut przywraca pierwotne OC.
6. **Wynik i krzywa UV** otwiera porównanie i CSV sesji. Podczas optymalizacji i podglądu ustawienia UV są tymczasowe. Po zakończeniu możesz oddzielnie zapisać zwycięzcę do wolnego slotu przyciskiem **Zapisz UV w MSI…**.

Domyślny katalog sesji na Twoim komputerze to `%LOCALAPPDATA%\RTX-UV-Cyberpunk\sesje`. W katalogu sesji `kopia-MSI-i-VF.zip` zawiera kopię. `oryginalna-krzywa.csv` i `wybrana-krzywa.csv` mają napięcia w mV i częstotliwości w MHz. Zachowaj katalog sesji do końca pracy procesu odzyskiwania.

## Weryfikacja

EXE skompilowano dla Windows x64. Testy programowe na Linuxie obejmują przeliczanie frametime, odrzucanie niespójnego eksportu, naturalny rozrzut, kroki napięcia, granice krzywej, odrzucanie niezgodnego slotu MSI lub napięcia ponad pułap oraz zapis kolejnych stanów sesji. Kod Windows przeszedł analizę statyczną i kompilację. Dodatkowo wykonano próbę zapisu, odczytu i przywrócenia z użyciem symulowanej tabeli sterownika oraz testy odrzucania błędnego odczytu, zmiany limitów, utraty watchdoga i niezgodnego dziennika. Test z mutowanymi buforami sprawdzał parser danych sterownika. W tym środowisku nie uruchomiono sesji z prawdziwą kartą NVIDIA ani Cyberpunkiem.

Kolejne elementy wymagają sprawdzenia na fizycznym Windows 11 z RTX 4070 SUPER i zainstalowaną grą.

| Próba | Kryterium na komputerze |
| --- | --- |
| Benchmark w trybie „Zmierz obecny OC” | Gra uruchamia scenę, zamyka się, tworzy oba eksporty i daje trzy poprawne wyniki przy Twoich ustawieniach. |
| Odczyt MSI / NVAPI | Pełna tablica offsetów zgadza się z profilem zapisanym w MSI. Zegary, napięcie, Hot Spot i PerfCap są dostępne w danej wersji sterownika. Przy braku prywatnego odczytu PerfCap aplikacja pokazuje oficjalne powody limitowania zegara NVML. |
| Tymczasowa próba przed UV | Zapis i odczyt V/F działają. Zarówno odtworzenie niezależne, jak i ponowne wczytanie istniejącego slotu MSI odtwarzają oryginał punkt po punkcie. |
| Odzyskanie po awarii na danym koncie | Przerwanie gry lub zatrzymanie aplikacji wyzwala weryfikację odtworzenia. Po ponownym logowaniu działa własny wpis Autostart, bez konieczności uruchamiania MSI z wyłączoną krzywą wentylatorów. |
| Rzeczywisty UV | Karta osiąga niższe mierzone napięcie. Zegary i FPS mieszczą się w granicach bazowych podczas pętli godzinowej. |
| Test przekroczenia pułapu napięcia | Odczyt wyższy od maksimum z bazowych benchmarków kończy sesję i uruchamia odtworzenie oryginalnego OC. |

W tym środowisku nie było fizycznego GPU, sterownika ani instalacji gry. Nie ma gotowych wyników dla Twojej karty.

## Audyt wymagań

Status „w kodzie” oznacza ścieżkę znalezioną w aplikacji po kompilacji. Działanie odczytów i zapisów sterownika, rzeczywiste przebiegi gry, działanie watchdoga przy awarii oraz odzyskiwanie po ponownym zalogowaniu wymagają testu na docelowym Windows 11.

| Wymaganie | Wynik audytu |
| --- | --- |
| Jeden parametr strojenia | W kodzie zmienną planera jest napięcie kotwicy. Taktowanie docelowe jest stałe podczas wyszukiwania. Sterownik otrzymuje offsety częstotliwości na istniejącej siatce V/F, żeby karta mogła osiągać ten sam zegar przy niższym napięciu. |
| Aktualna krzywa OC Scanner +165 MHz | W kodzie. Aplikacja zapisuje pełny odczyt krzywej oraz widoczne pliki konfiguracji i profile MSI. Porównuje rozpowszechniony format zapisanego slotu z odczytem. Wartość +165 MHz z interfejsu MSI nie jest osobno wykrywana i wymaga wybrania właściwego wcześniejszego slotu. |
| VRAM, Power Limit, Temperature Limit i wentylatory | Własna ścieżka zapisu dotyczy tablicy V/F rdzenia. Parametry pamięci, mocy i temperatury są kontrolowane przez odczyt porównawczy. Ustawienia wentylatorów są sprawdzane po konfiguracji MSI i aplikacja wymaga uruchomionego MSI. Odtwarzanie korzysta też z numeru oryginalnego slotu. |
| Kroki 25 mV i 12,5 mV | W kodzie i testach jednostkowych. Planer wybiera najbliższe dostępne punkty. Po pierwszym odrzuceniu stosuje drobniejszy krok, zawęża przedział i sprawdza sąsiedztwo granicy. |
| Odczyt po zapisie i benchmark Cyberpunk 2077 | W kodzie. Każda testowana krzywa w optymalizacji przechodzi odczyt i sprawdzenie przed uruchomieniem sceny z `-benchmark`. Próba odtwarzania przed optymalizacją i ręczny podgląd krzywej są osobnymi trybami. |
| Brak zmian GPU w trakcie testu | W kodzie. Proces gry kończy się przed przejściem do zapisu następnej krzywej lub do odtworzenia OC. W trakcie sceny aplikacja odczytuje telemetrię i weryfikuje profil. |
| Porównywalna temperatura startowa | W kodzie. Cel dobrany w bezczynności po rozgrzewce co najmniej 45 s po zakończeniu sceny. W badanych próbach pięć kolejnych próbek mieści się w ±2°C względem wspólnego celu. |
| Grafika, DLSS, Frame Generation, Path Tracing i rozdzielczość | Aplikacja nie zapisuje ustawień gry i porównuje odcisk `UserSettings.json`. Sam odcisk pliku nie identyfikuje nadpisań zewnętrznych ustawień sterownika, modów i nakładek. |
| Procesy w tle | Lista rozpoznawanych programów GPU, okresowa kontrola ich obecności oraz próg obciążenia CPU i GPU przed pomiarem. Aplikacja może wykryć wpływ innych procesów po zmianie rozrzutu kontroli OC. |
| Telemetria i awarie | Odczyt zegara, VRAM, napięcia, mocy, temperatury, obciążenia i dostępnych czujników Hot Spot i PerfCap. Samo niskie obciążenie podczas ładowania nie przerywa pomiaru. Na zakończenie procesu gry obowiązuje limit 4 min od uruchomienia. Spadki FPS są oceniane po pomiarze, a TDR i błędy sterownika na podstawie dziennika systemowego i utraty odczytu. |
| STOP i odzyskiwanie po błędzie | Kod anuluje proces gry, kończy sesję i przechodzi do oryginalnego profilu MSI. Przy nierozstrzygniętym odzyskaniu blokuje następną optymalizację zapisanym stanem. Po poprawnie zmierzonej regresji można próbować kolejnego punktu, po odtworzeniu OC. |
| Niezależny watchdog i restart Windows | W kodzie. Drugi proces otrzymuje dane oryginalnego OC i śledzi stan procesu głównego. Proces główny monitoruje żywotność watchdoga. Odzyskiwanie po restarcie korzysta z tymczasowego wpisu folderu Autostart. Po zalogowaniu możliwe jest tylko odzyskiwanie oryginału. Brak potwierdzenia odtwarzania utrzymuje blokadę. |
| Blokada zegara lub napięcia w bezczynności | Ścieżka zapisu zmienia tablicę krzywej V/F. Bezpośrednie blokady zegara i napięcia nie są wywoływane. Zmiany siatki napięć, firmware, VBIOS, zabezpieczeń, rejestru TDR i limitów fabrycznych są poza zakresem tej ścieżki. |
| Rzeczywiste taktowanie, FPS i naturalny rozrzut | W kodzie i testach logicznych. Ocena używa średniego FPS, 1% Low, 0,1% Low, P99 i zegara. Dopuszczalny spadek mediany zegara w próbie UV ma dodatkowy pułap 30 MHz nawet przy wysokim rozrzucie odniesienia. |
| Okresowe próby oryginału i końcowy benchmark | W kodzie. Porównanie w doborze, finałach, mniej więcej co 10 minut sceny w teście wydłużonym oraz po wczytaniu i weryfikacji oryginału na końcu. Awaryjne zatrzymanie kończy się odzyskiwaniem, bez kolejnej próby gry. |
| Pięć benchmarków i godzina testu | W kodzie. Profil z pierwszym odrzuconym pomiarem odpada od razu. Zakwalifikowany finalista wymaga pięciu zaliczonych przebiegów. Dla finalisty z przewagą nad OC liczy się suma 60 minut sceny Cyberpunk, bez przerw i ładowania w sumie. |
| Dry Run | W kodzie i testach planera. Sprawdza zapisany slot, symuluje decyzje tego samego planera, wykonuje jeden benchmark na aktywnym OC i sprawdza uruchomienie watchdoga oraz odczyt oryginalnego OC. Faktyczne odtworzenie z błędnego profilu jest sprawdzane w oddzielnej próbie po uzyskaniu zgody na optymalizację. |
| Próba odtwarzania przed właściwym UV | W kodzie. Próba obniżenia maksymalnego fragmentu krzywej o najwyżej 30 MHz i powrót do kopii przez NVAPI oraz przez oryginalny slot MSI. Niezgodność blokuje wyszukiwanie. |
| Priorytety oceny i pozostawienie OC przy remisie | W kodzie. Najpierw wymagania zachowania sprzętu i brak błędów. Wśród zakwalifikowanych kandydatów kolejność porównania to FPS, 1% Low, wahania zegara, pobór i temperatura. Remis pozostawia oryginalny profil. |
| Trwałość i zgoda | Eksperymentalne zastosowanie funkcjonuje tylko w bieżącej sesji albo po ręcznym potwierdzeniu maksymalnie pięciominutowego podglądu. CSV jest raportem. Oddzielny zapis zwycięzcy do wolnego slotu wymaga potwierdzenia po sesji. Zapis nie ustawia UV jako domyślnego i nie powoduje automatycznego stosowania po restarcie. |
| Zakres dowodu stabilności | Zaliczenie pętli godzinowej opisuje tę grę i wybraną konfigurację. Brakuje prób na fizycznej RTX 4070 SUPER z docelowym sterownikiem, wersją MSI i instalacją gry. |

## Kompilacja

Kod w folderze `source` korzysta z Go 1.24+ i standardowych bibliotek Windows. Uruchom `Kompiluj.cmd` przy dostępnym poleceniu `go` w PATH. Gotowy EXE jest niepodpisany. Możesz porównać jego SHA-256 z dołączonym plikiem albo zbudować z kodu.

## Podstawa techniczna

- [Dokumentacja systemowego mechanizmu TDR i jego parametrów](https://learn.microsoft.com/en-us/windows-hardware/drivers/display/tdr-registry-keys)
- [Instrukcja MSI Afterburner, OC Scanner i obsługa slotów](https://www.msi.com/support/technical_details/VGA_MSI_Utility_AfterBurner)
- [Przykład polecenia MSI /ProfileN](https://github.com/AzimsTech/amitxv-PC-Tuning/blob/main/docs/post-install.md) — zachowanie polecenia i jego współpraca z uruchomionym MSI są sprawdzane w próbie odtworzenia. Program pozostawia MSI uruchomiony podczas pomiarów i weryfikacji odtwarzania.
- [Publicznie opisany format plików MSI i krzywej V/F 2.0](https://pkg.go.dev/github.com/hekmon/aiup/overclocking/msiaf)
- [Publiczna dokumentacja NVIDIA NVAPI](https://docs.nvidia.com/nvapi/group__gpuapi.html) i [NVIDIA NVML](https://docs.nvidia.com/deploy/nvml-api/)
- [Układ tabeli V/F w implementacji Windows](https://github.com/vuplea/simple-nvidia-undervolt/blob/main/src/NvApi.cs), [starsza implementacja Windows nvapioc](https://github.com/Demion/nvapioc/blob/master/Source/main.cpp) i [odczyt czujników NVIDIA w Libre Hardware Monitor](https://github.com/LibreHardwareMonitor/LibreHardwareMonitor/blob/master/LibreHardwareMonitorLib/Hardware/Gpu/NvidiaGpu.cs) — warstwa V/F, Hot Spot i część odczytów profilu używają rzeczywistych, prywatnych funkcji NVIDIA. Zgodność ich struktur zależy od wersji sterownika.
- [Automatyczne uruchamianie sceny poleceniem -benchmark](https://www.pcgamingwiki.com/wiki/Cyberpunk_2077), [lokalizacja wyników według CD PROJEKT RED](https://support.cdprojektred.com/pl/cyberpunk/pc/sp-technical/issue/1775/problemy-z-wydajnoscia), [struktura katalogu i pliki benchmarku](https://www.principledtechnologies.com/Microsoft/RTXSpark-vs-M5Pro-1026/)
