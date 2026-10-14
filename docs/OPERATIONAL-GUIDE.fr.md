# Guide Opérationnel : Agent Bus Monitor (`agent-bus`)

Ce guide pratique est conçu en complément du `README.md`. Il fournit une marche à suivre pas à pas pour installer, configurer et utiliser `agent-bus-monitor`, le gestionnaire d'espaces de travail `herdr`, son plugin `herdr-plus`, ainsi que le pilotage quotidien d'équipes d'agents IA sur un projet neuf ou existant.

---

## 1. Vue d'ensemble & Fonctions Principales

`agent-bus-monitor` est un bus de coordination multi-agents asynchrone et découplé, bâti sur **Redis Streams** et outillé en **Go**. Il permet à plusieurs agents autonomes (Claude Code, OpenAI Codex, Kimi CLI, etc.) de collaborer sur un même projet avec une visibilité totale pour l'humain.

```
┌─────────────────────────────────────────────────────────────┐
│                       Opérateur Humain                      │
│                  (TUI busmon + herdr tabs)                  │
└──────────────┬──────────────────────────────▲───────────────┘
               │ @agent directive             │ Flux live / Board d'avancement
               ▼                              │
┌─────────────────────────────────────────────┴───────────────┐
│              Broker Redis (Streams sur :6380)               │
│   {p}:cmd  ·  {p}:status  ·  {p}:report  ·  {p}:board       │
└───────▲──────────────────────▲──────────────────────▲───────┘
        │                      │                      │
┌───────┴────────┐     ┌───────┴────────┐     ┌───────┴────────┐
│     master     │     │     coder      │     │    foureyes    │
│  (Coordinateur)│     │ (Implémenteur) │     │ (Revue 4-yeux) │
└────────────────┘     └────────────────┘     └────────────────┘
```

### Les Composants Majeurs

1. **Le Broker Redis (`redis:8-alpine`)**
   - Isole les données par espace de nom de projet (`{project}:{stream}`).
   - Gère les flux :
     - `{p}:cmd` : directives et commandes adressées.
     - `{p}:status` : états déclarés et pulsations passives.
     - `{p}:report` : comptes-rendus synthétiques destinés aux humains.
     - `{p}:notify` : annonces globales d'information.
     - `{p}:verdicts` : registre d'audit des approbations et refus.
   - Maintient les structures d'état : `{p}:board` (registre des tâches), `{p}:pilot` (bail du coordinateur), `{p}:armed:<agent>` (baux d'écoute active), et `{p}:gate:<agent>` (défis de revue 4-yeux).

2. **Le CLI `agentbus` (`cmd/agentbus`)**
   - Outil en ligne de commande permettant aux agents et aux scripts d'interagir avec le bus :
     - Émettre un état / pulsation : `agentbus status <agent> <working|idle|blocked|waiting|done> <message>`
     - Assigner des ordres : `agentbus cmd [--ref T] <agent> <directive>`
     - Publier un rapport d'étape : `agentbus report <agent> <note>`
     - Consulter le tableau de bord partagé : `agentbus board`
     - Valider les verrous 4-yeux : `agentbus gate`, `agentbus verdict`

3. **Le Dashboard TUI `busmon` (`cmd/busmon`)**
   - Interface terminal interactive pour superviser la ruche d'agents en temps réel :
     - **STATUS** : État du broker, master actif, requêtes en attente, état du dispatch.
     - **AGENTS** : Puces d'état de chaque agent (`working`, `idle`, `blocked`, `done`), temps depuis la dernière mise à jour, baux d'écoute `👂`, challenges ouverts `🔒`.
     - **BOARD** : Avancement des tâches en cours (`N/M done`).
     - **ACTIVITY** : Flux chronologique des échanges et rapports avec consultation détaillée (`Enter`).
     - **INPUT** : Barre de commande pour parler à l'équipe (`@coder ...`).

4. **L'Event-Driven Dispatcher (`agentbus dispatch`)**
   - Service d'arrière-plan surveillant le flux `{p}:cmd`.
   - Lorsqu'une directive arrive pour un agent au repos et désarmé, il injecte une commande de réveil unique dans son volet `herdr` (uniquement si le volet est libre et non focalisé).
   - L'agent se réveille, dépile ses tâches via `agentbus subscribe --drain <agent>`, s'exécute, puis retourne en sommeil.

---

## 2. Avantages de l'Architecture

- **Zéro coût de jetons au repos** : Grâce au dispatcher événementiel, un agent inactif ne consomme aucun token en boucle de veille inutile ; il est réveillé uniquement lorsqu'une tâche concrète lui est assignée.
- **Claude par défaut, adaptable à tout modèle** : Le projet utilise Claude Code par défaut (`roles.toml`), mais supporte nativement des équipes mixtes avec OpenAI Codex ou Kimi CLI via des profils configurables (`docs/AGENT-PROFILES.md`).
- **100% local, prêt pour le distant** : Fonctionne entièrement en local sur votre machine (`127.0.0.1:6380`), tout en étant nativement conçu pour relier des serveurs distants (VDR, serveurs GPU) via un simple tunnel SSH chiffré.
- **Observabilité et gouvernance** : Visibilité immédiate pour l'humain via le TUI `busmon`, combinée à une barrière de relecture obligatoire à 4 yeux (`foureyes`, `security`).

---

## 3. Installation Pas à Pas depuis le Terminal

Tout passe par `./setup.sh`, lancé depuis un clone du dépôt. Chaque étape peut être relancée
sans risque.

### Étape 1 : Prérequis Système

- **Go** : la version exigée par `go.mod`, actuellement 1.26.3.
- **Docker** et **Docker Compose**, pour le broker Redis.
- **Git** et **curl**.

Python n'est plus nécessaire depuis la v0.10.2.

### Étape 2 : Cloner et installer

```bash
git clone https://github.com/netbja/agent-bus-monitor ~/agent-bus-monitor
cd ~/agent-bus-monitor
./setup.sh install --dry-run          # ce qui manque, ce qui changerait (ne modifie rien)
./setup.sh install --install-herdr    # l'installation réelle
```

`install` :
- vérifie les outils ;
- compile `agentbus` et `busmon`, dans le dépôt et dans `~/.local/bin`, avec leur numéro de
  version ;
- installe **herdr** s'il manque (`--install-herdr`) ;
- enregistre **herdr-plus** auprès de herdr (`herdr plugin install cloudmanic/herdr-plus --ref <tag>`)
  et vérifie que son action Projects existe ;
- ajoute le raccourci `Ctrl+B` puis `↑`. Il crée le `config.toml` de herdr si herdr n'a jamais
  tourné, et demande à un herdr déjà ouvert de le relire ;
- clone les skills externes (`mattpocock/skills`, version épinglée) dans
  `~/.local/share/agentbus/skills/mattpocock` et lie les skills des rôles. S'il en manque un,
  l'agent démarre sans lui, avec un avertissement ;
- démarre le broker Redis sur `127.0.0.1:6380`.

Vérifiez que `~/.local/bin` est dans votre `PATH` : `install` prévient s'il n'y est pas, ou si une
autre copie passe devant.

### Étape 3 : Vérifier, mettre à jour, désinstaller

```bash
./setup.sh status       # versions, PATH, skills, herdr-plus, raccourci, Redis
./setup.sh update       # git pull --ff-only puis install ; affiche vA -> vB et les notes de version
./setup.sh uninstall    # demande à chaque étape ; --yes = choix prudents ; --purge = données Redis aussi
```

Quand une version plus récente existe, busmon l'indique discrètement dans son en-tête :
`↑ vX.Y.Z`. La vérification a lieu une fois par jour ; `AGENT_BUS_NO_UPDATE_CHECK=1` la
désactive.

`update` refuse un dépôt avec des changements non commités, puis lance le `setup.sh` qu'il vient
de tirer. Une nouvelle version épinglée de herdr-plus réinstalle celui que setup a installé (un
dossier local lié n'est jamais remplacé).

`uninstall` ne retire que ce que setup a installé. Il n'a pas de `--dry-run` : lancez-le sans
`--yes` et répondez à chaque question. Il retire :
- les services de tous les projets (leurs unités ; données et journaux gardés) ;
- nos binaires et nos liens de skills (jamais un vrai dossier) ;
- le raccourci, s'il est encore exactement le bloc écrit par setup ; un bloc modifié est gardé ;
- les skills externes, si setup les a clonés et qu'ils n'ont pas de changements locaux.

Il pose une question à part pour herdr-plus, qui reste utile sans agent-bus. Pour Redis, il
propose : l'arrêter en gardant les données (le choix par défaut), le supprimer avec ses données,
ou ne rien toucher.

---

## 4. Topologie Réseau : Local vs Distant

### Déploiement 100% Local (par défaut)
Tous les processus tournent sur la même machine. Les agents et `busmon` se connectent directement à `127.0.0.1:6380` (mot de passe par défaut : `AgentBus2025!`).

### Déploiement Hybride avec Serveur Distant (VDR / GPU)
Le broker Redis reste confiné au loopback (`127.0.0.1:6380`) pour des raisons de sécurité. La connexion distante s'opère via un tunnel SSH sécurisé :

- **Cas A : Vous surveillez depuis votre machine un bus tournant sur un serveur distant** :
  ```bash
  ssh -NL 6381:localhost:6380 utilisateur@serveur-distant &
  REDIS_PORT=6381 busmon --project mon-projet
  ```
- **Cas B : Les agents distants rejoignent le bus de votre machine de développement** :
  Depuis le serveur distant, ouvrez le tunnel inverse :
  ```bash
  ssh -L 6380:localhost:6380 utilisateur@machine-locale -N
  ```
  Les agents sur le serveur distant interagissent alors naturellement avec le bus comme s'il était local.

---

## 5. Marche à Suivre : Lancement et Gestion des Projets

### Cas A : Démarrer une Équipe sur un NOUVEAU Projet

Votre projet est dans `~/code/myapp`.

1. **Lancer herdr** (une session par projet, c'est pratique) :
   ```bash
   herdr --session myapp
   ```

2. **Créer et ouvrir l'équipe en une seule commande**, dans herdr :
   ```bash
   agentbus up myapp ~/code/myapp --as <votre-nom>
   ```
   Cette commande :
   - démarre le broker Redis si besoin, lie les skills et écrit le modèle herdr-plus (c'est
     `scripts/bootstrap` en dessous ; ses options marchent aussi : `--no-dispatch`,
     `--no-missions`, `--index`, `--cron`) ;
   - démarre les **services** du projet avec le gestionnaire de services du système (systemd
     `--user` sous Linux, launchd sous macOS) :
     - `dispatch` : réveille les agents inactifs quand du travail arrive ;
     - `missions` : le conductor des gros changements.

     Ils ne vivent pas dans un onglet : fermer busmon, l'espace de travail ou herdr ne les arrête
     pas. Sans gestionnaire utilisable, `up` le dit et les lance en onglets ; `--tabs` force ce
     mode ;
   - ouvre directement l'espace de travail, avec **busmon en premier et au premier plan**,
     puis :
     - `master` : coordinateur de l'équipe ;
     - `coder` : agent d'implémentation ;
     - `foureyes` : agent de relecture ;
     - `sentinel` : surveillance de la santé et du budget.
   - Relancée, elle remet au premier plan l'espace déjà ouvert au lieu d'en ouvrir un second.

   busmon vous affiche comme `♙ <Nom> [human]`, avec `· pilot` tant que vous tenez le bail
   pilote. Sans `--as`, vous êtes `Pilot [human]`. Le nom s'écrit en minuscules, comme celui d'un
   agent : `--as bernard` affiche `Bernard`. `export AGENT_BUS_HUMAN=bernard` le fixe pour tous
   les busmon.

3. **Donner la première tâche** depuis la ligne de saisie de busmon (`i`) :
   ```text
   @master Initialise la structure du projet et prépare la feuille de route.
   ```
   `@agent` réveille cet agent, `@all` les réveille tous. Un texte sans `@` est une diffusion
   que personne ne lit : busmon demande une seconde Entrée avant de l'envoyer.

---

### Cas B : Reprendre un Projet EXISTANT

1. Lancez herdr (`herdr --session myapp`).
2. Lancez `agentbus up myapp`, ou faites `Ctrl+B` puis `↑` et choisissez `myapp`.
   > Les onglets s'ouvrent avec de nouvelles sessions d'agents. Pour reprendre une conversation
   > Claude Code précédente, utilisez `claude --resume`, puis choisissez `myapp:master` ou
   > `myapp:coder`.

   Le sélecteur ne rouvre que l'espace de travail. Après un redémarrage de la machine, rien n'a
   relancé les services : `agentbus up myapp` les démarre, et busmon affiche `dispatch down`
   d'ici là.

---

### Cas B2 : Passer une équipe d'avant la v0.12

Une équipe dont dispatch et missions tournaient dans des onglets, ou montée à la main (ses propres
noms d'onglets et d'agents, sans template herdr-plus) :

1. **Les services seuls, depuis un terminal hors de herdr.** Hors de herdr, `agentbus up`
   installe et démarre les services sans rien ouvrir : il n'ajoute jamais une seconde équipe à
   côté de la vôtre.
   ```bash
   agentbus up myapp ~/code/myapp --session <session-herdr> --escalate-to <coordinateur>
   ```
   - `--session` : la session herdr où vivent les agents, quand son nom n'est pas celui du
     projet. Le dispatcher y cherche leurs onglets.
   - `--escalate-to` : l'agent qui reçoit les escalades et les avis `complete`, quand le
     coordinateur ne s'appelle pas `master`. Le choix est enregistré : un `agentbus up myapp`
     ultérieur le garde.
2. **Fermez les anciens onglets** qui lançaient `agentbus dispatch`, `agentbus missions` ou une
   ancienne boucle de réveil : le dispatcher doit être le seul à écrire dans les onglets.
3. **Chaque agent se déclare une fois** depuis son onglet
   (`agentbus --project myapp status <nom> idle "back"`), ou le coordinateur le fait pour
   l'équipe. Tant qu'un agent ne l'a pas fait, le dispatcher ne connaît pas son onglet et n'y
   écrit jamais.

---

### Cas C : Surveiller et Arrêter un Projet

- **Surveiller.** L'en-tête de busmon affiche `dispatch ready`, `dispatch waiting herdr`,
  `missions idle`, `missions busy`, `missions failed`, etc. `F3` (ou `s`) ouvre la fenêtre
  Services, avec la dernière erreur et la fin de chaque journal.
- **Un agent qui vous attend.** Quand master (ou un autre agent) a besoin de votre décision, il
  déclare `waiting` : il passe en tête d'Agents avec ♙ et le mot `waiting` qui s'estompe et revient lentement, l'en-tête affiche `♙ 1
  waiting for you`, et la cloche du terminal sonne une fois par nouvelle question, ce que herdr
  signale sur l'onglet busmon. Répondez avec `@master …` ; l'agent repasse ensuite en `working`.
- **La jauge `updated`** d'Agents (`●●●●●●●○○○ 3m`) indique la fraîcheur du dernier état déclaré
  par l'agent : pleine juste après la déclaration, vide au bout de 10 minutes, puis l'âge seul.
  Elle ne mesure pas un travail en cours.
- **Défilement automatique d'Activity.** Le titre d'Activity indique `auto-scroll on` tant que les
  nouvelles lignes défilent. `f` le coupe (la vue reste en place et compte ce qui arrive :
  `auto-scroll off · 3 new`) et le rétablit ; se déplacer, cliquer ou ouvrir une ligne le laisse actif. La
  molette vers le haut le coupe (vous relisez), `Fin` le rétablit. Le séparateur du jour s'affiche `Wed 2026-10-14 - (today)`.
- **Les agents disparus** (une équipe remplacée, un rôle renommé) gardent leur dernier état
  déclaré : rien ne devine qu'un agent est mort. busmon retire du panneau Agents, et de `@all`, un
  agent silencieux depuis plus d'un jour qui n'écoute pas et à qui rien n'est dû ; le panneau les
  compte (`3 dormant`) et `a` les affiche. Pour en retirer un définitivement, sélectionnez-le et
  faites `d` (ou `Suppr`), puis `y` : c'est `agentbus --project myapp agents forget <nom>`. Un
  agent qui écoute, ou qui a déclaré un état dans les 10 dernières minutes, est refusé.
- **Les vieilles requêtes.** Le compteur Requests ne porte que sur les dernières 24 heures : une
  directive plus ancienne restée sans réponse ne réveille personne. Dans le panneau Requests,
  `d` puis `y` clôt un fil par une réponse de votre part ; son historique reste. Une requête
  suivie est close par son destinataire (`agentbus request done`).
- **Lire les journaux :** `agentbus --project myapp service logs missions -n 100`. Ils tournent
  à 1 Mio, trois fichiers gardés.
- **Arrêter :** `agentbus down myapp` arrête les services et ne supprime rien. Les agents et
  l'espace de travail restent ouverts ; fermez-les dans herdr. Si le conductor vérifiait ou
  intégrait, ses tests sont tués avec leur groupe de processus, et il refait cette étape à son
  prochain démarrage.
- **Redémarrer un service :** `agentbus --project myapp service restart dispatch`, par exemple
  après `./setup.sh install`, car un service en cours garde son ancien binaire.
- **Pannes.** Un service qui échoue est relancé. Après 5 échecs en 10 minutes, il abandonne et
  busmon affiche `gave up` ; le prochain `agentbus up` réessaie. Il n'est jamais relancé en
  boucle sur une configuration invalide, un projet supprimé, ou quand une autre instance tient
  déjà son bail.
- **Supprimer.** `busmon --delete myapp` retire aussi les services du projet sur cette machine.
  `./setup.sh uninstall` retire d'abord les services de tous les projets.

---

## 6. Démarrer le Travail : une Petite Tâche, un Grand Changement

L'équipe est lancée (`agentbus up myapp …`) et busmon est ouvert. Vous pouvez lui confier du
travail de deux façons, selon sa taille.

| | Petite tâche | Grand changement |
|---|---|---|
| Taille | d'une heure à une journée ; une fonctionnalité, un correctif | plusieurs jours ; plusieurs agents en parallèle |
| Qui pilote | `master`, dans sa conversation | le **conducteur** (service `missions`), à partir d'un plan dans Git |
| Ce qui survit à un redémarrage | le tableau (board) et les messages du bus | tout : le plan est dans Git, son état dans Redis |
| Ce que vous faites | écrire la demande, répondre aux questions | valider le plan, trancher les escalades, merger le résultat |

### Petite tâche : demander à `master`

1. **Écrivez la demande dans busmon** (`i`), avec l'objectif et la façon dont vous jugerez que c'est fini :
   ```text
   @master Add CSV export to the reports page. Done when: an "Export" button downloads the
   current filter as CSV, with tests, reviewed by foureyes, on a branch ready for a PR.
   ```
   Vous pouvez aussi écrire directement dans l'onglet de master ; busmon est simplement plus
   rapide à atteindre.
2. **master découpe et distribue.** Il envoie une directive à `coder`, qui réclame la tâche sur
   le tableau et travaille sur sa branche, puis `foureyes` relit (règle des 4 yeux : l'auteur
   n'approuve jamais son propre travail).
3. **Suivez** le panneau Board et le fil Activity. Un agent qui a besoin de vous écrit à master,
   et master vous pose la question.
4. **Terminez.** master annonce la branche et le verdict de revue
   (`agentbus --project myapp verdicts`). Vous ouvrez ou validez la PR.

Pour un correctif d'une ligne, vous pouvez passer outre master : `@coder Fix the typo in the README title.`

### Grand changement : le planifier en missions, laisser le conducteur piloter

Un changement de plusieurs jours ne doit pas dépendre de la conversation d'un agent : un
contexte se remplit, une session redémarre. Le plan vit donc dans Git, et le conducteur
l'exécute étape par étape.

1. **Demandez un plan, pas du code :**
   ```text
   @master Plan the change "auth-v2" as missions: write docs/changes/auth-v2/brief.md and
   docs/changes/auth-v2/missions.json on a branch, commit, and send me the commit. Do not start it.
   ```
   Pour un changement qui demande surtout de la conception, faites d'abord lancer l'architecte
   par master (`scripts/agent-spawn architect myapp`).
2. **Relisez le plan.** Dans `missions.json`, chaque mission nomme son `agent`, son `reviewer`
   (jamais le même agent), ses `depends_on`, les `allowed_paths` qu'elle peut toucher, son
   `test` et une section du `brief`. Le changement a aussi un `test` (la suite complète) et, en
   option, `"docs": "docs"` pour que l'agent de documentation la mette à jour à la fin.
   `docs/changes/roles-go/` est un exemple réel. Demandez des corrections jusqu'à ce que le plan
   soit juste : c'est le moment où le corriger coûte le moins.
3. **Chargez-le** au commit relu (vous ou master) :
   ```bash
   agentbus --project myapp mission load docs/changes/auth-v2/missions.json@<commit>
   ```
   À partir de là, le conducteur :
   - envoie chaque mission à son agent dès que ses dépendances sont intégrées (les missions
     indépendantes avancent en parallèle) ;
   - vérifie chaque livraison : seulement les chemins autorisés, les tests de la mission au vert ;
   - ouvre la revue avec le relecteur de la mission ;
   - merge la mission approuvée dans `change/auth-v2` et lance la suite du changement.
4. **Suivez-le** dans busmon (`missions busy`, `missions idle`), ou en détail :
   ```bash
   agentbus --project myapp mission status auth-v2
   agentbus --project myapp mission show auth-v2/t3
   ```
5. **Tranchez quand une mission bloque** (tests en échec, revue refusée, trop de tentatives).
   Le conducteur escalade à master, qui décide ou vous demande :
   `mission retry`, `mission reassign <c>/<id> <agent>`, `mission skip … --reason`, `mission abort`.
   Pour changer le plan, committez un nouveau `missions.json` et rechargez-le avec
   `mission load` : les missions inchangées gardent leur état.
6. **Sur plusieurs jours.** Les services continuent quand vous fermez busmon, les agents ou herdr.
   Fermez tout le soir si vous voulez. Le lendemain, `agentbus up myapp` rouvre l'équipe. Les
   missions qui attendent un agent lui sont remises dès que son onglet est revenu. Après un
   redémarrage de la machine, c'est aussi `agentbus up myapp` qui relance les services. Un agent
   dont le contexte est plein peut être relancé : le brief de sa mission et sa branche suffisent.
7. **La fin.** Quand toutes les missions sont intégrées, master reçoit un avis `complete` (et
   l'agent de documentation sa directive, si le plan en nomme un). Le conducteur ne merge jamais
   `change/auth-v2` dans `main` : ouvrez cette PR vous-même, ou demandez-la à master, et mergez-la.

---

## 7. Synthèse des Commandes Essentielles

| Action | Commande |
|---|---|
| Créer et ouvrir une équipe | `agentbus up mon-app ~/code/mon-app --as <votre-nom>` |
| Arrêter les services d'un projet | `agentbus down mon-app` |
| État et journaux des services | `agentbus --project mon-app service status` / `service logs [dispatch\|missions]` |
| Mettre à jour les outils | `./setup.sh update` |
| Voir ce qui est installé | `./setup.sh status` |
| Retirer les outils | `./setup.sh uninstall` |
| Afficher le dashboard live | `busmon --project mon-app --as <votre-nom>` |
| Lister les projets sur le bus | `busmon --list` |
| Vérifier les agents enregistrés | `agentbus --project mon-app agents` |
| Consulter le registre des tâches | `agentbus --project mon-app board` |
| Envoyer un ordre direct | `agentbus --project mon-app cmd coder "Corriger le test unitaire"` |
| Consulter l'historique d'un ordre | `agentbus --project mon-app thread <id-ou-ref>` |
| Lister les comptes-rendus | `agentbus --project mon-app reports` |
| Ajouter un agent en renfort | `scripts/agent-spawn architect mon-app` (depuis le master) |
| Purger l'historique d'un projet | `busmon --project mon-app --reset` |

---

## 8. Bonnes Pratiques & Pièges Fréquents

1. **`AGENT_BUS_PROJECT` obligatoire** : Pensez à exporter `export AGENT_BUS_PROJECT=<nom>` dans vos shells d'agents pour ne pas répéter `--project`.
2. **Format des options** : Utilisez toujours un double tiret et un espace (`--ref abc123`, jamais `--ref=abc123` ni `-ref abc123`).
3. **Ordre positionnel strict** : Les arguments suivent un ordre précis : `agentbus status <agent> <state> <message>`. Le message se place toujours en dernier.
4. **Nommage normalisé** : Les noms de projets et d'agents doivent respecter l'expression régulière `^[a-z][a-z0-9_-]{0,31}$` (minuscules, débutant par une lettre).
