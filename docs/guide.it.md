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

La configurazione guidata del nodo (`./setup.sh`, oppure `setup.bat` su Windows) scrive un profilo
del wallet per il nodo appena configurato (`wallet.toml`, vedi la
[sezione 4](#4-profili-il-nodo-locale-la-porta-diag-e-il-controllo-della-rete)) e, per default, fa
della chiave del nodo l'account del wallet: se il nodo ha già una chiave la usa così com'è; se non
ne ha ancora una, la crea subito da una frase di 24 parole (vedi
[La chiave del nodo](#la-chiave-del-nodo-il-wallet-di-un-nodo)). Un account separato, per i
risparmi, è l'altra scelta.

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
4. **Sblocca un account.** L'interfaccia si apre sulla schermata di sblocco: nomi e indirizzi
   degli account, nient'altro. Scegline uno con le frecce, premi Invio e scrivi la sua password;
   solo allora vedi saldo, cronologia, token e contratti. Vedi
   [Sbloccare e bloccare](#sbloccare-e-bloccare).

### Sbloccare e bloccare

- **Una password per sessione.** Sbloccato un account, nell'interfaccia invii, deleghe e chiamate
  ai contratti non chiedono più la password; su mainnet scrivi comunque `send` sul riepilogo
  completo. La riga di comando è diversa: ogni comando chiede la password quando firma.
- **Blocca** con `l`; **cambia account** con `a` (il wallet prima blocca, poi chiede la password
  dell'altro account). Dopo aver creato o importato un account il wallet torna anch'esso alla
  schermata di sblocco, sul nuovo account.
- **Blocco automatico**: dopo 10 minuti senza tasti premuti il wallet si blocca da solo (mai mentre
  una transazione è in viaggio). Si cambia con `lock_after_minutes = N` in cima a `config.toml`;
  `0` non blocca mai: chi arriva al tuo terminale (una sessione SSH lasciata aperta, uno schermo
  non bloccato) può firmare con l'account sbloccato. Tienilo breve sulle macchine raggiungibili da
  altri.
- Una password sbagliata dice solo che l'account non si è potuto sbloccare; ogni tentativo
  sbagliato di fila aggiunge una breve attesa (2 s, poi 4, 8, fino a 30 s).
- Da sbloccata, la chiave resta in memoria protetta dallo swap e fuori dai core dump, e viene
  azzerata quando il wallet si blocca o esce. Se il sistema lo rifiuta ("the key could not be
  locked in memory: it may reach swap"), usa uno swap cifrato. Alcune copie un programma Go non
  può cancellarle (quello che la firma calcola dalla chiave, la password digitata): disattiva i
  core dump sulle macchine che custodiscono fondi.

In Docker il wallet gira dentro il container del nodo, con i suoi dati nel volume `/data`
(`/data/wallet`); imposta `CREDITS_API=on` in `.env` (il wallet parla con l'API del nodo). Con il
`docker-compose.yml` del progetto (servizio `node`, container `credits-node`):

```sh
docker compose exec node credits-wallet       # oppure: docker exec -it credits-node credits-wallet
```

---

## 2. Account, frase di recupero e backup

### Creare un account

Nell'interfaccia premi `n`; da riga di comando:

```sh
./credits-wallet account new main
```

1. Scegli una **password**. Cifra il file della chiave su questa macchina (Argon2id e
   XChaCha20-Poly1305). L'interfaccia la chiede per sbloccare l'account; i comandi, a ogni
   transazione.
2. Il wallet mostra **una sola volta** la **frase di recupero di 24 parole**. Scrivila su carta,
   in ordine.
3. Riscrivi le tre parole che ti chiede.
4. Il wallet chiede `Save the 24 words to a file as well? [y/N]` (salvare le 24 parole anche in un
   file?). **Il default è no**: premi Invio e le parole restano solo sul tuo foglio. Vedi
   [Il file di recupero](#il-file-di-recupero).

Viene mostrato l'indirizzo dell'account (la sua chiave pubblica, in base58): dallo a chi ti invia CS.

**Copiare il tuo indirizzo.** Nella schermata principale, e dopo aver creato o importato un
account, `y` copia negli appunti l'indirizzo completo dell'account (OSC 52: funziona anche via SSH,
verso gli appunti del computer che stai usando). L'indirizzo è anche mostrato per intero su una riga
a sé: se il tuo terminale blocca OSC 52, tieni premuto Shift e trascina per selezionarlo. Da riga
di comando `account address NAME` stampa solo l'indirizzo, da passare agli appunti:

```sh
./credits-wallet account address main | xclip -selection clipboard   # Linux (X11; wl-copy su Wayland)
./credits-wallet account address main | pbcopy                       # macOS
.\credits-wallet.exe account address main | clip                     # Windows
```

### La frase di recupero

- Le 24 parole **sono** l'account: chi le ha può spendere tutto quello che contiene, da qualsiasi
  wallet, senza password. Senza di esse nessuno può recuperare l'account.
- Scrivile su carta, mai in una e-mail, in una chat, in una foto o in un gestore di password nel
  cloud. Un file solo di passaggio, vedi sotto. Tieni il foglio dove fuoco e acqua non arrivano, meglio se in due posti.
- Il wallet poi pulisce lo schermo, ma lo scrollback di un terminale, tmux/screen o il log di una
  sessione possono conservare le parole: chiudi la finestra del terminale e pulisci lo scrollback
  di una sessione che resta aperta a lungo.
- La frase ha lo stesso formato di quella del nodo: `account import NAME` e scrivere le 24 parole
  ripristina l'account, su questa o su un'altra macchina.

### Il file di recupero

Se alla domanda dopo la verifica rispondi `y` (da riga di comando o nell'interfaccia), il wallet
scrive le 24 parole anche in un file:

- Di default `~/credits-recovery-NOME.txt` (su Windows `%USERPROFILE%\credits-recovery-NOME.txt`);
  se preferisci scrivi un altro percorso (Invio tiene quello proposto; `~/` è la tua cartella home).
- Il file contiene il nome dell'account, il suo indirizzo, la data, le 24 parole numerate e un
  avviso. Viene creato nuovo, leggibile solo da te (permessi 600; su Windows quelli del profilo
  utente), e non sostituisce mai un file esistente né segue un collegamento simbolico. Su Linux
  compare intero o non compare affatto. Su macOS e Windows il wallet scrive prima
  `credits-recovery-NOME.txt.partial` lì accanto e lo rinomina quando è completo: se il computer si
  blocca o va via la corrente in quei pochi millisecondi, **cancella il file `.partial`** (contiene
  le parole). Finché c'è, il wallet si rifiuta di scrivere.
- **Non sta mai accanto alle chiavi.** Un percorso nella cartella dei dati del wallet, o in una
  cartella che contiene file di chiavi, o che sta sotto una di queste, viene rifiutato e il wallet
  chiede di nuovo. Sono file di chiavi: i file delle chiavi del wallet e le loro esportazioni
  cifrate, `NodePrivate.txt` e le sue copie, una chiave segreta in base58 (`export --format
  base58`), il JSON delle chiavi dei wallet ufficiali e i collegamenti a uno di questi. Viene
  rifiutata anche la cartella del nodo, pure mentre il wallet vi sta creando la chiave del nodo. Anche una
  cartella che il wallet non riesce a leggere viene rifiutata. La cartella dei dati viene
  riconosciuta anche sotto un altro nome (un collegamento, una junction, maiuscole diverse su
  macOS). Chi copia la
  cartella dei dati ottiene i file delle chiavi cifrati, che richiedono ancora la tua password; con
  le parole lì accanto la password non proteggerebbe più nulla.
- **Portalo via dalla macchina.** Il wallet poi dice: *This file holds your account in clear: copy
  it to paper or an offline USB key, then delete it from this computer.* (il file contiene il tuo
  account in chiaro: copialo su carta o su una chiavetta USB tenuta offline, poi cancellalo da
  questo computer). Fallo subito: un file lasciato sul computer può essere letto da un malware, da
  un servizio di backup o da chiunque metta le mani sul disco.

Con `sudo` il wallet usa la home dell'utente con cui gira sia per la cartella dei dati sia per
questo file. Se `$HOME` appartiene a un altro utente (`sudo` senza `-H`) o il wallet gira come
root, te lo segnala.

La domanda non viene fatta con `--json`, con `--words-file` o `--show-words`, né senza un
terminale: gli script continuano a usare `--words-file FILE` (lo stesso meccanismo di scrittura,
con le sole parole su una riga).

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

- una chiave privata in base58;
- il JSON delle chiavi dei wallet ufficiali (sezione successiva);
- la chiave del nodo stesso, `NodePrivate.txt` (base58), o la frase di 24 parole del nodo.

Dopo aver importato da un file un JSON delle chiavi o una chiave base58, cancella il file in chiaro:
la chiave è ormai nell'archivio cifrato delle chiavi.

Accanto a un nodo non serve importare la chiave del nodo: il wallet la usa dove si trova (sezione
successiva). Una copia importata è cifrata nell'archivio delle chiavi, ma l'originale resta in
chiaro per il nodo, quindi la copia non protegge niente di più. **Non cancellare mai
`csnode/NodePrivate.txt`: il nodo ne ha bisogno per funzionare.**

### La chiave del nodo (il wallet di un nodo)

Il nodo ha una sua coppia di chiavi: al suo indirizzo arrivano le ricompense del nodo e le deleghe
fatte a lui, e di solito chi gestisce un nodo vi tiene già il proprio stake. Accanto al suo nodo il
wallet è **il wallet del nodo** e usa direttamente quella chiave, come account `node`:

- quando parte con il `wallet.toml` del pacchetto (`./credits-wallet --config wallet.toml`, nella
  cartella `csnode`): il suo profilo dice `node_key = "NodePrivate.txt"`;
- quando un profilo del tuo `config.toml` ha `node_key = "/percorso/di/csnode/NodePrivate.txt"`
  (la configurazione guidata lo scrive con il percorso completo quando crea `config.toml`);
- con `--node-key /percorso/di/csnode/NodePrivate.txt` sulla riga di comando;
- nell'immagine Docker del nodo (`CREDITS_WALLET_NODE_KEY=/data/keys/NodePrivate.txt`).

`node_key = "off"` nel profilo lo disattiva. Il wallet non la deduce mai dalla cartella in cui lo
avvii: una cartella controllata da altri potrebbe contenere una coppia di chiavi messa lì apposta,
e finiresti per dare in giro il suo indirizzo. Prima di usare la chiave controlla che
`NodePrivate.txt` e la sua cartella siano tuoi (o di root) e che nessun altro possa scriverci (si
corregge con `chmod 600 NodePrivate.txt` e `chmod go-w` sulla cartella; la configurazione guidata
fa entrambe le cose quando scegli la chiave del nodo), e la prima volta che la legge mostra
l'indirizzo dell'account e il file da cui viene: controlla che siano quelli del tuo nodo. Un file
di chiave che altri utenti possono **leggere** viene usato, con un avviso: il nodo scrive la sua
chiave leggibile da tutti (modo 644), quindi esegui il `chmod 600` indicato dall'avviso. Su
Windows questi controlli non vengono fatti (`account node-key` lo dice): controlla tu Proprietà >
Sicurezza del file.

L'account `node` è il primo della lista ed è selezionato all'avvio dell'interfaccia; è segnato
**NODE KEY** nella lista, nella schermata dell'account, in `account list` e in ogni riepilogo. Il
wallet legge `NodePrivate.txt` dove lo legge il nodo: la chiave **non viene copiata**
nell'archivio delle chiavi del wallet e **non viene cifrata** dal wallet (al nodo serve il suo
file), quindi una chiave in chiaro non chiede password, né allo sblocco né per firmare; su mainnet
scrivi comunque `send`. `account
node-key` mostra il file, l'indirizzo, se `NodePublic.txt` corrisponde e se il nodo è in
esecuzione. (Se l'archivio ha già un account di nome `node`, da un'importazione precedente, la
chiave del nodo si chiama `node-key` e l'altro mantiene il suo nome.)

> **La chiave del nodo resta in chiaro sul server**, perché il nodo ne ha bisogno: i CS che vi
> tieni sono esposti quanto il server. Tienici solo quello che serve al nodo (il suo stake), e i
> risparmi in un account separato (`account new`).

**Una chiave cifrata dal nodo.** Se il nodo è stato avviato con `--encryptkey`, o al primo avvio
hai scelto "[1] Encrypt with password", `NodePrivate.txt` è cifrato con la password del nodo. Il
wallet lo apre con quella stessa password: nella schermata di sblocco dell'interfaccia, e da riga
di comando quando un comando firma (funziona anche `--password-file`). Fino ad allora l'account
`node` mostra l'indirizzo di `NodePublic.txt`, segnato "encrypted by the node", e il wallet
controlla che la chiave aperta sia proprio quella; finché non la sblocchi, `account list`, `account
address` e la schermata della chiave trovata segnano l'indirizzo "(from NodePublic.txt, unverified
until unlocked)". L'apertura richiede qualche secondo e **1 GiB di memoria libera**, come nel nodo:
con meno, il wallet rifiuta con "the node's key needs 1 GiB of free memory to open (as the node
does)" (chiudi qualcosa, o aprila su una macchina con più memoria). Il wallet non scrive mai la chiave aperta da nessuna parte e rifiuta di
esportarla in chiaro. Il wallet apre entrambi i formati del nodo: un file cifrato ora dal nodo ha un
proprio salt casuale; uno cifrato da un nodo più vecchio usa un salt comune a tutti i nodi, quindi
scegli una password lunga (cifrarlo di nuovo con un nodo attuale gli dà un salt proprio).

**Una chiave nella cartella in cui avvii il wallet.** Se avvii l'interfaccia in una cartella che
contiene `NodePrivate.txt` (il tuo `csnode`, per esempio) e non è configurata nessuna chiave del
nodo, il wallet lo dice nella prima schermata con l'indirizzo e il percorso completo della chiave,
e chiede: `u` usala per questa sessione, `r` usala e ricordala (scrive `node_key` con il percorso
completo nel tuo profilo in `config.toml`, lasciando invariato tutto il resto), `i` ignorala, `x`
ignorala e non chiedere più per quel file. Non la usa mai da solo, e la rifiuta quando altri utenti
possono scrivere nel file o nella sua cartella. Controlla l'indirizzo prima di sceglierla. I comandi
stampano solo un suggerimento: `This folder holds a node key: to use it, --node-key
NodePrivate.txt`.

**Il nodo non ha ancora una chiave.** Una cartella nuova del nodo non ha `NodePrivate.txt`; il nodo
ne creerebbe una al primo avvio facendoti domande sulla sua console. Creala invece con il wallet,
da una frase di 24 parole che è la copia su carta dell'identità del nodo:

```sh
./credits-wallet --config wallet.toml account node-key new      # nella cartella csnode
```

oppure `g` nella schermata principale dell'interfaccia. Come per `account new`, il wallet mostra le
24 parole una volta, ne chiede indietro tre e propone il file di recupero (default no; mai nella
cartella del nodo). Poi scrive `NodePrivate.txt` e `NodePublic.txt` nella cartella del nodo
esattamente come li scrive il nodo, leggibili solo da te (modo 600). Il nodo li usa al prossimo
avvio senza domande. Il wallet rifiuta, prima di mostrare qualsiasi parola, se c'è già un file di
chiave (o un link con quel nome), se il nodo è in esecuzione, o se la cartella appartiene a un
altro utente o chiunque può scriverci. `--out CARTELLA` indica la cartella di un altro nodo. Le 24
parole ripristinano la chiave: con `account import`, o con l'opzione `--seed` del nodo stesso.

**La chiave di un account come chiave del nodo.** `account export NOME --format node --out
CARTELLA_CSNODE` scrive un account del wallet come `NodePrivate.txt` e `NodePublic.txt` del nodo
(viene chiesta la password dell'account; scrivi `export` per confermare). Non scrive mai sopra una
chiave che il nodo ha già. Per **sostituire l'identità del nodo**, ferma il nodo e aggiungi
`--replace`: il wallet conserva i vecchi file come `NodePrivate.txt.bak-DATA` e
`NodePublic.txt.bak-DATA` (modo 600) e ti chiede di scrivere `replace`. **L'identità del nodo
segue la sua chiave**: il suo indirizzo, le deleghe ricevute, la sua storia di nodo fidato e le
sue ricompense appartengono alla chiave; un nodo avviato con un'altra chiave è un altro nodo.
**Ferma prima il nodo**: il wallet cerca un nodo in esecuzione (il suo `db/.lock`, la sua porta
API, su Linux il suo processo), ma solo come protezione; un nodo avviato con altre impostazioni o
da un altro utente non viene visto, e un nodo che resta acceso tiene la vecchia chiave fino al
prossimo avvio. Le copie contengono la vecchia chiave in chiaro: quando sei sicuro, copiale offline
(carta, una chiavetta USB scollegata) e cancellale dal server. `account node-key` le elenca.

**Una chiave scritta a metà.** Se `NodePrivate.txt` c'è ma `NodePublic.txt` manca o non
corrisponde (un crash durante la scrittura, un file copiato a mano), il nodo si ferma all'avvio e
chiede sulla sua console: un nodo senza console resta bloccato. Con il nodo fermo,
`./credits-wallet --config wallet.toml account node-key repair` scrive `NodePublic.txt` da
`NodePrivate.txt` (un file che non corrispondeva viene conservato come `NodePublic.txt.bak-DATA`).

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
con un profilo per il nodo che ha configurato (la sua porta API, la sua porta diag, mainnet e,
quando scegli la chiave del nodo, `node_key = "NodePrivate.txt"`: la chiave del nodo è l'account
`node`; altrimenti `node_key = "off"`, e un valore impostato a mano resta alle esecuzioni
successive), e lo copia nella cartella
dei dati come `config.toml` se non ce n'è ancora uno (con il percorso completo di
`NodePrivate.txt`). Un `config.toml`
esistente non viene mai modificato: `credits-wallet --config wallet.toml` usa invece il file della
configurazione guidata.

### Il controllo della rete

Mainnet è il default: il wallet la presume, a meno che il profilo dica `testnet` o `private`. Su
mainnet le schermate sono rosse e ogni transazione ti chiede di scrivere `send` (e, da riga di
comando, la password).

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

**La porta diag e il tuo firewall.** I pacchetti hanno la porta diag **attiva** (`diag_port=9088`),
e il nodo vi ascolta su **ogni interfaccia**, come per l'API principale, non solo su `127.0.0.1`.
L'API diagnostica ha anche comandi che cambiano il comportamento del nodo: **chiudi con il firewall
la 9088 (e la 9090) a ogni altro host**; il wallet le usa solo su `127.0.0.1`. La configurazione
guidata del nodo non attiva mai la porta diag al posto tuo. Se
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
nodo ha confermato la rete. Su mainnet scrivi `send` (da riga di comando segue la password). Il wallet segue la
transazione finché non è in un blocco.

- Controlla **l'intero** indirizzo del destinatario, non solo le prime e le ultime lettere.
- **Incollare il destinatario.** Usa l'incolla del terminale (Ctrl+Shift+V, clic destro, Cmd+V su
  macOS) nel campo del destinatario e in ogni campo che vuole un indirizzo (un nodo a cui delegare,
  un token, un contratto). Spazi, a capo e un `credits:` iniziale vengono tolti; tutto ciò che non è
  un indirizzo (base58 di 32 byte) viene rifiutato e il campo resta com'era. L'indirizzo incollato è
  mostrato con i primi e gli ultimi 6 caratteri evidenziati: confrontali, insieme al resto, con il
  punto da cui l'hai copiato. Alcuni malware sostituiscono negli appunti gli indirizzi con i propri.
  Il wallet non legge mai gli appunti da solo (Ctrl+V nei suoi campi non fa nulla).
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
`s` invia il token, `a` aggiunge un token tramite il suo indirizzo, `x` lo toglie dal tuo elenco. La schermata principale mostra
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
In quel caso l'elenco viene dalla cache, più i token dell'elenco locale dell'account (quelli che hai
aggiunto, o inviato con il wallet) che la cache non nomina. Senza la cache, il wallet:

- usa la cache dei token dell'`history_node` del profilo, se ne ha uno (i saldi vengono comunque
  letti dal tuo nodo);
- altrimenti scorre la cronologia dell'account (le ultime 1000 transazioni: i token che ha inviato,
  chiamato o comprato, e quelli che ha ricevuto) e il suo elenco locale (i token che hai aggiunto o
  inviato), e legge ogni saldo dal contratto. Un token che l'account ha solo ricevuto è segnato
  "received from INDIRIZZO: not added by you": chiunque può inviarti un token, anche un'imitazione. Un elenco così è etichettato **"found from
  history (may be incomplete on a pruned node)"**: i token che altri ti hanno inviato non sono
  nella tua cronologia, e un nodo pruned conserva solo i blocchi recenti. Aggiungili con `a` o
  `token add`.

La cache ha un costo: al primo avvio con la cache attiva, il nodo la ricostruisce (cerca i token
tra tutti i contratti pubblicati e le loro transazioni, e ci vuole un po'), e poi legge i saldi dei
token in background dopo ogni chiamata a un token.

**La cache è completa solo se era attiva mentre il nodo leggeva tutti i blocchi.** Un nodo full con
`tokens_cache=true` fin dall'inizio conosce tutti i possessori, e li conserva anche se più tardi fa
pruning. Un nodo pruned, o avviato dallo snapshot pubblicato, che attiva la cache più tardi elenca
tutti i token, e i saldi che mostra sono corretti, ma può non conoscere i wallet che hanno usato un
token l'ultima volta prima del suo blocco più vecchio: per loro il token non compare finché non lo
usano di nuovo. Se manca un token che possiedi, aggiungilo con `a` o `token add` (il saldo viene
allora letto dal contratto).

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
- **Blocca il wallet** (`l`) prima di lasciare il terminale, e tieni breve `lock_after_minutes`.
- **La chiave del nodo è in chiaro sul server** (al nodo serve così): tienici solo quello che
  serve al nodo, e i risparmi in un account separato, con le sue 24 parole su carta.
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
| "the node's key file is encrypted by the node" | Il nodo è stato avviato con `--encryptkey` (o al primo avvio è stata scelta una password): scrivi la password del nodo (nella schermata di sblocco, o quando il comando la chiede; `--password-file` negli script). |
| "the node's key needs 1 GiB of free memory to open" | Aprire una chiave cifrata dal nodo richiede 1 GiB, come nel nodo: libera memoria e riprova. |
| "could not unlock" | La password è sbagliata (o il file della chiave è danneggiato). Aspetta i secondi indicati e riprova; per l'account `node` di una chiave cifrata è la password del nodo. |
| "is encrypted by the node and NodePublic.txt does not give its address" | `NodePublic.txt` manca o è danneggiato: neanche il nodo partirebbe. Ripristinalo da un backup o con il nodo. |
| "This folder holds a node key" | Hai lanciato un comando in una cartella con `NodePrivate.txt`: il wallet non la usa se non aggiungi `--node-key NodePrivate.txt` (o imposti `node_key`). |
| "the node is running" (`node-key new`, `--format node`) | Ferma prima il nodo: il wallet non scrive mai la chiave del nodo mentre è in esecuzione. |
| "NodePrivate.txt already exists" | Il nodo ha una chiave: il wallet non ci scrive mai sopra. Per sostituire di proposito l'identità del nodo: `account export NOME --format node --out CARTELLA --replace`. |
| Manca l'account `node` | Il wallet non è il wallet del nodo: avvialo con `--config wallet.toml` nella cartella `csnode`, o imposta `node_key` nel profilo (oppure `--node-key FILE`). |
| "the node's key … can be written by other users" / "belongs to another user" | `chmod 600 NodePrivate.txt`, `chmod go-w` sulla cartella del nodo; avvia il wallet come l'utente proprietario della cartella del nodo. |
| Token "found from history (may be incomplete on a pruned node)" | Il nodo non ha una cache dei token. Attiva `tokens_cache=true` in `[api]` del suo `config.ini` e riavvialo, oppure aggiungi il token con `token add`. |
| Manca un token anche se la cache è attiva | La cache è stata attivata dopo il pruning, o il nodo è partito da uno snapshot: conosce tutti i token, ma non i wallet che li hanno usati l'ultima volta prima del suo blocco più vecchio. Aggiungi il token con `token add` (il suo saldo viene letto dal contratto). Anche il primo avvio del nodo con la cache attiva richiede un po' (log: `tokens loaded!`). |
| "the executor lost the errors" durante la compilazione | L'esecutore di contratti del nodo non ha restituito le righe di errore; correggi il sorgente con un IDE oppure aggiorna l'esecutore del nodo. |
| "sent, but … check history later" | Non inviare di nuovo. Controlla `history` tra un minuto: la transazione è in un blocco, oppure è scaduta e puoi inviare di nuovo. |
| Schermo illeggibile | Usa un terminale con UTF-8 e almeno 80×24 (Windows Terminal su Windows). |
| Incollando un indirizzo compare "not a CREDITS address" | Quello che hai incollato non è un indirizzo completo: copialo di nuovo, per intero. Se l'indirizzo copiato è giusto e quello incollato è diverso, qualcosa su questo computer ha cambiato gli appunti: non inviare. |
| `y` non copia nulla | Il tuo terminale non supporta OSC 52 (oppure tmux richiede `set -g set-clipboard on`): tieni premuto Shift e trascina sull'indirizzo per selezionarlo. |
| macOS: "cannot be opened" | `xattr -d com.apple.quarantine credits-wallet` (oppure `xattr -dr com.apple.quarantine csnode` per l'intero pacchetto). |
| Docker: gli account sono spariti | La cartella dei dati deve essere nel volume: l'immagine imposta `CREDITS_WALLET_HOME=/data/wallet`; esegui il wallet con `docker exec` nel container che ha il volume. |

Problemi e domande: [issues](https://github.com/CreditsCommunity/wallet/issues) (non pubblicare mai
le tue 24 parole, una chiave privata o un JSON di chiavi).
