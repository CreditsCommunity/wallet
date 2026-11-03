# Guida di credits-wallet

credits-wallet è un wallet da terminale per la rete CREDITS. Gira sulla stessa macchina di un nodo,
su Linux, Windows e macOS, in una finestra di terminale o via SSH. Tiene le tue chiavi sulla tua
macchina, firma da solo ogni transazione e invia al nodo solo transazioni firmate. Non apre nessuna
porta e non richiede un browser.

Questa guida copre l'uso di tutti i giorni. Il riferimento completo è
[docs/spec.md](https://github.com/CreditsCommunity/wallet/blob/main/docs/spec.md) e
`credits-wallet help`. La versione in inglese è in `guide.md`.

> **Mainnet significa denaro vero.** Ogni importo che invii su mainnet è definitivo. Leggi
> [Consigli di sicurezza](#8-consigli-di-sicurezza) prima del tuo primo trasferimento.

Indice

1. [Installazione e primo avvio](#1-installazione-e-primo-avvio)
2. [Account, frase di recupero e backup](#2-account-frase-di-recupero-e-backup)
3. [Importare una chiave dal vecchio wallet ufficiale](#3-importare-una-chiave-dal-vecchio-wallet-ufficiale)
4. [Profili, il nodo locale, la porta diag e il controllo della rete](#4-profili-il-nodo-locale-la-porta-diag-e-il-controllo-della-rete)
5. [Invio di CS, deleghe, token e cronologia](#5-invio-di-cs-deleghe-token-e-cronologia)
6. [Smart contract](#6-smart-contract)
7. [Script](#7-script)
8. [Consigli di sicurezza](#8-consigli-di-sicurezza)
9. [Risoluzione dei problemi](#9-risoluzione-dei-problemi)

---

## 1. Installazione e primo avvio

### Dal pacchetto del nodo della community (consigliato)

Dalla release community.17 in poi, ogni pacchetto del
[nodo della community](https://github.com/CreditsCommunity/node/releases) include il wallet nella
cartella `csnode`, accanto al nodo:

| Pacchetto | Wallet |
|---|---|
| `credits-node-…-linux-x64.tar.gz`, `…-linux-x64-glibc2.31.tar.gz` | `csnode/credits-wallet` |
| `credits-node-…-windows-x64.zip` | `csnode\credits-wallet.exe` |
| `credits-node-…-macos-arm64.tar.gz` | `csnode/credits-wallet` |
| Immagine Docker `ghcr.io/creditscommunity/credits-node` | `credits-wallet` nel `PATH` |

Questa guida è nel pacchetto come `wallet-guide.md` (e `wallet-guide.it.md`).

La configurazione guidata del nodo (`./setup.sh`, oppure `setup.bat` su Windows) alla fine propone
di creare o importare un account, con un profilo del wallet per il nodo appena configurato (vedi
la [sezione 4](#4-profili-il-nodo-locale-la-porta-diag-e-il-controllo-della-rete)).

### Da solo

Ogni release del wallet su [GitHub](https://github.com/CreditsCommunity/wallet/releases) ha un
archivio per ogni sistema e un file `SHA256SUMS`:

```sh
# Linux x86-64 (binario statico: qualsiasi distribuzione)
sha256sum -c --ignore-missing SHA256SUMS
tar -xzf credits-wallet-0.1.0-linux-amd64.tar.gz
cd credits-wallet-0.1.0 && ./credits-wallet version
```

- **macOS (Apple Silicon)**: `shasum -a 256 -c --ignore-missing SHA256SUMS`; il binario non è
  firmato, quindi togli una volta la quarantena del download: `xattr -d com.apple.quarantine credits-wallet`.
- **Windows (x64)**: in PowerShell, `Get-FileHash credits-wallet-0.1.0-windows-amd64.zip` e
  confronta il risultato con `SHA256SUMS`; estrai lo zip ed esegui `credits-wallet.exe` da Windows
  Terminal (o PowerShell, cmd). SmartScreen può avvisarti che il programma non è firmato.

I binari sono riproducibili: lo stesso tag compilato di nuovo dà gli stessi file (vedi il README).

### Primo avvio

1. **Avvia il nodo e lascialo sincronizzare.** Il wallet parla con l'API del nodo su `127.0.0.1:9090`
   (`[api] port` nel `config.ini` del nodo; 9090 nei pacchetti).
2. **Controlla il nodo:**

   ```sh
   ./credits-wallet status
   ```

   Mostra la rete, l'ultimo blocco, se il nodo è `synced` e cosa dice il nodo sulla propria rete.
   Il wallet rifiuta di inviare finché il nodo non è sincronizzato.
3. **Apri l'interfaccia da terminale:**

   ```sh
   ./credits-wallet
   ```

   La riga in alto mostra la rete (MAINNET in rosso) e il nodo. `?` mostra i tasti, `q` esce.
   Ogni funzione esiste anche come comando (`credits-wallet help`).

In Docker il wallet gira dentro il container del nodo, con i suoi dati nel volume `/data`:

```sh
docker exec -it credits-node credits-wallet
```

---

## 2. Account, frase di recupero e backup

### Creare un account

Nell'interfaccia premi `n`; da riga di comando:

```sh
./credits-wallet account new main
```

1. Scegli una **password**. Cifra il file della chiave su questa macchina (Argon2id e
   XChaCha20-Poly1305). Viene chiesta a ogni transazione.
2. Il wallet mostra **una sola volta** la **frase di recupero di 24 parole**. Scrivila su carta,
   in ordine.
3. Riscrivi le tre parole che ti chiede.

Viene mostrato l'indirizzo dell'account (la sua chiave pubblica, in base58): dallo a chi ti invia CS.

### La frase di recupero

- Le 24 parole **sono** l'account: chi le ha può spendere tutto quello che contiene, da qualsiasi
  wallet, senza password. Senza di esse nessuno può recuperare l'account.
- Scrivile su carta, mai in un file, in una e-mail, in una chat, in una foto o in un gestore di
  password nel cloud. Tieni il foglio dove fuoco e acqua non arrivano, meglio se in due posti.
- Il wallet poi pulisce lo schermo, ma lo scrollback di un terminale, tmux/screen o il log di una
  sessione possono conservare le parole: chiudi la finestra del terminale e pulisci lo scrollback
  di una sessione che resta aperta a lungo.
- La frase ha lo stesso formato di quella del nodo: `account import NAME` e scrivere le 24 parole
  ripristina l'account, su questa o su un'altra macchina.

### Backup

| Cosa | Come | Nota |
|---|---|---|
| La frase | su carta | ripristina tutto, senza la password |
| Il file della chiave | `account export main --out main.key` | copia cifrata; serve la password |
| La cartella dei dati | copiala | file delle chiavi (cifrati), configurazione, elenchi locali di contratti e token |

La cartella dei dati è `~/.credits-wallet` su Linux e macOS, `%APPDATA%\credits-wallet` su Windows,
oppure la cartella indicata da `CREDITS_WALLET_HOME` (`/data/wallet` nell'immagine Docker). I file
delle chiavi sono in `accounts/`, leggibili solo da te.

**Una password dimenticata non si può recuperare**: importa di nuovo l'account dalle sue 24 parole,
con una nuova password (`account import main2`).

### Altre importazioni

`account import NAME FILE`, o `i` nell'interfaccia, accetta anche:

- la chiave del nodo stesso, `NodePrivate.txt` (base58), o la frase di 24 parole del nodo;
- una chiave privata in base58;
- il JSON delle chiavi dei wallet ufficiali (sezione successiva).

Cancella il file in chiaro dopo l'importazione: la chiave è ormai nell'archivio cifrato delle chiavi.

---

## 3. Importare una chiave dal vecchio wallet ufficiale

Il wallet web ufficiale (wallet.credits.com) e il wallet desktop salvano una chiave come file JSON:

```json
{"key":{"public":"<base58 public key>","private":"<base58 private key>"}}
```

Questo file **non è cifrato**: chi lo legge possiede l'account. Per spostarlo in credits-wallet:

```sh
./credits-wallet account import old ~/Downloads/keys.json
```

oppure `i` nell'interfaccia e il percorso del file nel campo Secret. Il wallet controlla che le due
metà appartengano alla stessa chiave, chiede una password e salva la chiave cifrata. Poi:

1. Controlla l'indirizzo mostrato: deve essere quello del tuo vecchio wallet.
2. Esegui `./credits-wallet balance old`: il saldo deve essere quello che conosci.
3. **Cancella il file JSON** (e svuota il cestino), e ogni sua copia nelle cartelle Download, nelle
   e-mail o nel cloud.

Per tornare a un wallet ufficiale, `account export old --format official --out keys.json` scrive lo
stesso JSON non cifrato, dopo un avviso.

---

## 4. Profili, il nodo locale, la porta diag e il controllo della rete

### Profili

Un profilo dà un nome a un nodo e alla sua rete. Senza file di configurazione il wallet usa un solo
profilo, `local`: il nodo su `127.0.0.1:9090`, su mainnet. `credits-wallet config` stampa un esempio
di `config.toml`; salvalo nella cartella dei dati per modificarlo:

```toml
node = "local"                 # il profilo usato per default

[nodes.local]
address = "127.0.0.1:9090"     # l'API del nodo ([api] port)
network = "mainnet"            # "mainnet", "testnet" or "private"
diag = "127.0.0.1:9088"        # l'API diagnostica del nodo ([api] diag_port); "off" per non interrogarla
# history_node = "203.0.113.7:9090"   # un full node remoto per la cronologia (sola lettura)

[nodes.test]
address = "127.0.0.1:9190"
network = "testnet"
```

Scegli un profilo con `--profile test` su qualsiasi comando. `--node HOST:PORT` parla con un altro
nodo, che conta sempre come **mainnet**.

La configurazione del nodo (`setup.sh` / `setup.ps1`) scrive `wallet.toml` nella cartella `csnode`
con un profilo per il nodo che ha configurato (la sua porta API, la sua porta diag, mainnet), e lo
copia nella cartella dei dati come `config.toml` se non ce n'è ancora uno. Un `config.toml`
esistente non viene mai modificato: `credits-wallet --config wallet.toml` usa invece il file della
configurazione guidata.

### Il controllo della rete

Mainnet è il default: il wallet la presume, a meno che il profilo dica `testnet` o `private`. Su
mainnet le schermate sono rosse e ogni transazione ti chiede di scrivere `send` e poi la password.

Un nodo della community dalla release community.17 in poi **dice anche su quale rete si trova**,
tramite la sua API diagnostica (`[api] diag_port`, 9088 nei pacchetti). Prima di ogni transazione il
wallet la interroga e **rifiuta di inviare** quando nodo e profilo non concordano: un profilo che
dice mainnet su un nodo di test, o viceversa. `status` mostra cosa dice il nodo:

```
network      : MAINNET (declared in the configuration; confirmed by the node)
node says    : MAINNET (uuid 11024959585341937636), community node 4.2.532-community.17
```

Quando il nodo non risponde (un nodo ufficiale, una release più vecchia, la porta diag disattivata
o chiusa), decide la configurazione e il riepilogo dice "not confirmed by the node". Per
richiedere la conferma, aggiungi `require_network_uuid = true` al profilo; `network_uuid = "…"`
fissa una rete (l'uuid che mostra `status`).

**La porta diag e il tuo firewall.** L'API diagnostica ha anche comandi che cambiano il
comportamento del nodo. Il nodo vi ascolta su ogni interfaccia, come per l'API principale:
**non aprire mai la 9088 (né la 9090) su Internet**; il wallet la usa solo su `127.0.0.1`. Se
imposti `diag_port=0`, il wallet funziona comunque, senza la conferma della rete.

---

## 5. Invio di CS, deleghe, token e cronologia

### Inviare CS

Nell'interfaccia: `s`, poi il destinatario, l'importo e un testo Data facoltativo. Da riga di
comando:

```sh
./credits-wallet send main RECIPIENT_ADDRESS 12.5
./credits-wallet send main RECIPIENT_ADDRESS 12.5 --data "invoice 42"
```

Il riepilogo mostra il destinatario per intero, l'importo, la commissione prevista (circa 0.0087 CS),
la **commissione massima (max fee)** firmata (1 CS: il massimo che il nodo può addebitare) e se il
nodo ha confermato la rete. Su mainnet scrivi `send`, poi la password. Il wallet segue la
transazione finché non è in un blocco.

- Controlla **l'intero** indirizzo del destinatario, non solo le prime e le ultime lettere.
- Il testo Data è **pubblico**: chiunque può leggerlo sulla blockchain.
- Se il wallet dice "sent, but … check history later", **non inviare di nuovo** prima che la
  cronologia mostri se il primo invio è arrivato.

### Deleghe

```sh
./credits-wallet delegate main NODE_ADDRESS 1000                     # senza limite di tempo
./credits-wallet delegate main NODE_ADDRESS 500 --until 2027-01-31   # fino a una data UTC
./credits-wallet undelegate main NODE_ADDRESS 1000                   # ritiro (solo senza limite)
./credits-wallet delegations main                                    # fatte e ricevute
```

Nell'interfaccia: `d`, poi `n` per delegare e `w` per ritirare la delega selezionata. Almeno 5 CS
ogni volta. Una delega con limite di tempo termina da sola alla sua data.

### Token

Un token è uno smart contract: **lo identifica solo l'indirizzo del suo contratto**. Chiunque può
pubblicare un token che si chiama "CS" o copia il nome di un altro token.

`t` nell'interfaccia elenca tutti i token che l'account possiede: codice, saldo e nome, e per il
token selezionato il suo **indirizzo completo del contratto**, i decimali e il saldo, con avvisi
quando il suo codice o nome assomiglia a CS o a un altro token che possiedi. `y` copia l'indirizzo
(nei terminali che supportano OSC 52; altrimenti tieni premuto Shift e trascina per selezionarlo),
`s` invia il token, `a` aggiunge un token tramite il suo indirizzo. La schermata principale mostra
i saldi e il numero di token, con `[t]`.

```sh
./credits-wallet tokens main                           # tutti i token, con il loro indirizzo
./credits-wallet token info TOKEN_ADDRESS              # codice, nome, decimali, supply
./credits-wallet token send main TOKEN_ADDRESS RECIPIENT 250.5
./credits-wallet token add main TOKEN_ADDRESS          # tieni un token nell'elenco
./credits-wallet token balance TOKEN_ADDRESS main      # un saldo, letto dal contratto
```

**Da dove viene l'elenco.** Il nodo tiene una cache dei token solo con `[api] tokens_cache=true`
nel suo `config.ini` (disattivata per default; la configurazione guidata propone di attivarla).
In quel caso l'elenco è completo per ciò che la cache del nodo conosce. Senza di essa, il wallet:

- usa la cache dei token dell'`history_node` del profilo, se ne ha uno (i saldi vengono comunque
  letti dal tuo nodo);
- altrimenti scorre la cronologia dell'account (le ultime 1000 transazioni: i token che ha inviato,
  chiamato o comprato) e il suo elenco locale (i token che hai aggiunto o inviato, o che una cache
  ha mostrato), e legge ogni saldo dal contratto. Un elenco così è etichettato **"found from
  history (may be incomplete on a pruned node)"**: i token che altri ti hanno inviato non sono
  nella tua cronologia, e un nodo pruned conserva solo i blocchi recenti. Aggiungili con `a` o
  `token add`.

La cache ha un costo: al primo avvio con la cache attiva, il nodo la ricostruisce (cerca i token
tra tutti i contratti pubblicati e le loro transazioni, e ci vuole un po'), e poi legge i saldi dei
token in background dopo ogni chiamata a un token. Su un nodo pruned, vedi
[Risoluzione dei problemi](#9-risoluzione-dei-problemi) per ciò che può mancare.

Un trasferimento di token costa circa 0.0175 CS più l'esecuzione; il saldo in CS deve coprire anche
la commissione massima firmata (1 CS), che il nodo trattiene mentre la chiamata è in esecuzione.

### Cronologia

`h` nell'interfaccia (`t` per i trasferimenti di token, `e` per esportare un CSV); da riga di
comando:

```sh
./credits-wallet history main
./credits-wallet history main --tokens
./credits-wallet history main --csv main.csv
```

Un nodo **pruned** conserva solo i blocchi recenti: il wallet dice da quale blocco parte la sua
cronologia. Per la cronologia più vecchia, indica nel profilo un full node di cui ti fidi
(`history_node = "HOST:PORT"`) oppure usa `--history-node HOST:PORT`. Quel nodo serve solo in
lettura: riceve l'indirizzo pubblico dell'account e non viene mai usato per inviare.

---

## 6. Smart contract

I contratti sono classi Java, compilate ed eseguite dall'esecutore di contratti del nodo. Il nodo
deve averlo attivo (i pacchetti lo hanno).

### Modelli

```sh
./credits-wallet contract templates           # Counter, TestToken, Token, TipJar, Faucet
./credits-wallet contract new Counter Counter.java
```

### Compilare

```sh
./credits-wallet contract compile Counter.java
```

Gli errori sono elencati come `FILE:LINE: message`. Nell'interfaccia (`c`, poi `o` per aprire un
file o `n` per un modello) il sorgente viene compilato all'apertura, le righe con errori sono
segnate, ed `e` lo apre nel tuo editor (`$EDITOR`, Notepad su Windows) e lo compila di nuovo quando
ritorni.

### Pubblicare (deploy)

```sh
./credits-wallet contract deploy main Counter.java
```

Il riepilogo mostra il futuro indirizzo del contratto, la commissione (circa 0.1 CS per un piccolo
contratto) e la commissione massima. Quando il deploy è in un blocco, il wallet aspetta che il nodo
elenchi il contratto e lo salva nell'elenco locale dei contratti dell'account (`contract list main`).

### Leggere, chiamare, pagare

```sh
./credits-wallet contract methods CONTRACT            # metodi e relativi tipi
./credits-wallet contract get CONTRACT getValue       # gratis: nulla viene firmato né conservato
./credits-wallet contract call main CONTRACT add 4    # firmata, circa 0.0175 CS + esecuzione
./credits-wallet contract pay main CONTRACT 3 --data "thanks"   # CS al suo payable()
./credits-wallet contract info CONTRACT               # deployer e stato pubblico
```

- **get** esegue un metodo senza transazione: usalo per tutto ciò che si limita a leggere.
- **call** firma una transazione; gli argomenti sono convertiti nei tipi dichiarati dal metodo
  (metti `--` prima di un argomento che inizia con `-`). Un metodo che lancia un'eccezione viene
  rifiutato dal nodo, e la commissione viene pagata.
- **pay** invia CS al contratto; il suo `payable()` può rifiutarli (tornano indietro, la
  commissione viene pagata).

Nell'interfaccia, su un contratto: Enter legge un getter o firma una chiamata, `g` esegue get, `c`
esegue call. Un riepilogo lungo scorre (PgDn): viene firmato solo dopo che lo hai visto fino in
fondo.

Tutto ciò che un contratto dice di sé (nomi, risultati, stato) è testo scritto dal suo autore: il
wallet lo mostra con i caratteri speciali neutralizzati (escaped), e non devi fidarti di più di quanto ti fidi
del suo autore.

---

## 7. Script

Ogni comando accetta `--json` per un output leggibile da macchina. Per inviare senza domande:

```sh
./credits-wallet send main ADDRESS 1 --yes --password-file ~/.wallet-pw
```

`--password-file` legge la password dalla prima riga di un file che deve essere `chmod 600`. La
password resta allora su disco in chiaro: usala solo dove l'invio non presidiato serve davvero, su
un disco cifrato e per un account che contiene poco. Senza un terminale il wallet rifiuta di
inviare senza `--yes`, e `account new` richiede `--words-file FILE` (oppure `--show-words`).

---

## 8. Consigli di sicurezza

- **Non condividere mai le 24 parole**, con nessuno, per nessun motivo: nessun addetto al supporto,
  amministratore o "airdrop" ne ha bisogno. Chi te le chiede sta rubando.
- **Account separati per le reti di test.** Una transazione firmata non dice per quale rete è: una
  firmata su una rete di test può essere riutilizzata (replay) su mainnet da chi gestisce o osserva quel nodo
  di test. Non usare mai altrove un account che contiene CS su mainnet. Dichiara i nodi di test come
  `network = "testnet"` (o `private`) nel loro profilo.
- **Invia prima una piccola somma** a un nuovo destinatario, e controlla che sia arrivata, prima
  di quella grande. Fai lo stesso con un nuovo token o contratto.
- **Controlla l'intero indirizzo** nella schermata di riepilogo, e l'indirizzo del contratto del
  token, non il suo nome.
- **Tieni pulita la macchina.** Il wallet protegge i file delle chiavi a riposo, non da malware o
  da un altro utente del tuo account di sistema: possono leggere il terminale o sostituire il
  binario. Usa una macchina che controlli, tienila aggiornata, disattiva i core dump e usa lo swap
  cifrato sulle macchine che custodiscono fondi.
- **Il tuo nodo.** Il wallet crede al nodo con cui parla per saldi, commissioni e cronologia.
  Usalo accanto al tuo nodo e tieni le porte del nodo (9090, 9088, 9080) chiuse agli altri host.
- **Scarica solo dalle release ufficiali** e controlla `SHA256SUMS`.

---

## 9. Risoluzione dei problemi

| Messaggio o sintomo | Cosa fare |
|---|---|
| `connection refused` su `127.0.0.1:9090` | Il nodo non è in esecuzione, oppure la sua API è su un'altra porta (`[api] port`): avvialo, oppure imposta `address` nel profilo. |
| `synced: NO` / "sending is refused" | Aspetta che il nodo raggiunga la testa della catena (`status`). |
| "not confirmed by the node" | Il nodo non dice la sua rete: è un nodo ufficiale, una release più vecchia, oppure la porta diag è disattivata (`diag_port=0`) o su un'altra porta (`diag` nel profilo). Decide la configurazione; controlla che la rete del profilo sia giusta. |
| "MISMATCH" / il wallet rifiuta di inviare | La rete del profilo e quella del nodo differiscono: sei sul nodo sbagliato o sul profilo sbagliato. Correggi il profilo; non forzare mai. |
| "wrong password" | Riprova. Password dimenticata: importa di nuovo l'account dalle sue 24 parole. |
| Token "found from history (may be incomplete on a pruned node)" | Il nodo non ha una cache dei token. Attiva `tokens_cache=true` in `[api]` del suo `config.ini` e riavvialo, oppure aggiungi il token con `token add`. |
| Manca un token anche se la cache è attiva | Su un nodo avviato da uno snapshot o pruned, la cache conosce solo i token e i possessori le cui transazioni sono nei blocchi che ha: aggiungi il token con `token add` (il suo saldo viene letto dal contratto). Anche il primo avvio del nodo con la cache attiva richiede un po' (log: `tokens loaded!`). |
| "the executor lost the errors" durante la compilazione | L'esecutore di contratti del nodo non ha restituito le righe di errore; correggi il sorgente con un IDE oppure aggiorna l'esecutore del nodo. |
| "sent, but … check history later" | Non inviare di nuovo. Controlla `history` tra un minuto: la transazione è in un blocco, oppure è scaduta e puoi inviare di nuovo. |
| Schermo illeggibile | Usa un terminale con UTF-8 e almeno 80×24 (Windows Terminal su Windows). |
| `y` non copia nulla | Il tuo terminale non supporta OSC 52 (oppure tmux richiede `set -g set-clipboard on`): tieni premuto Shift e trascina sull'indirizzo per selezionarlo. |
| macOS: "cannot be opened" | `xattr -d com.apple.quarantine credits-wallet` (oppure `xattr -dr com.apple.quarantine csnode` per l'intero pacchetto). |
| Docker: gli account sono spariti | La cartella dei dati deve essere nel volume: l'immagine imposta `CREDITS_WALLET_HOME=/data/wallet`; esegui il wallet con `docker exec` nel container che ha il volume. |

Problemi e domande: [issues](https://github.com/CreditsCommunity/wallet/issues) (non pubblicare mai
le tue 24 parole, una chiave privata o un JSON di chiavi).
