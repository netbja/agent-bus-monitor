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
     - Émettre un état / pulsation : `agentbus status <agent> <working|idle|blocked|done> <message>`
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
- ajoute le raccourci `Ctrl+B` puis `↑` ;
- clone les skills externes (`mattpocock/skills`, version épinglée) dans
  `~/.local/share/agentbus/skills/mattpocock` et lie les skills des rôles ;
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

`uninstall` retire :
- nos binaires et nos liens de skills (jamais un vrai dossier) ;
- le raccourci ;
- les skills externes.

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
   - ouvre directement l'espace de travail, avec **busmon en premier et au premier plan**,
     puis :
     - `master` : coordinateur de l'équipe ;
     - `coder` : agent d'implémentation ;
     - `foureyes` : agent de relecture ;
     - `sentinel` : surveillance de la santé et du budget ;
     - `dispatch` : réveille les agents inactifs quand du travail arrive ;
     - `missions` : le conductor des gros changements.
   - Relancée, elle remet au premier plan l'espace déjà ouvert au lieu d'en ouvrir un second.

   busmon vous affiche comme `♙ <Nom> [human]`, avec `· pilot` tant que vous tenez le bail
   pilote. Sans `--as`, vous êtes `Pilot [human]`.

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

---

## 6. Synthèse des Commandes Essentielles

| Action | Commande |
|---|---|
| Créer et ouvrir une équipe | `agentbus up mon-app ~/code/mon-app --as <votre-nom>` |
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

## 7. Bonnes Pratiques & Pièges Fréquents

1. **`AGENT_BUS_PROJECT` obligatoire** : Pensez à exporter `export AGENT_BUS_PROJECT=<nom>` dans vos shells d'agents pour ne pas répéter `--project`.
2. **Format des options** : Utilisez toujours un double tiret et un espace (`--ref abc123`, jamais `--ref=abc123` ni `-ref abc123`).
3. **Ordre positionnel strict** : Les arguments suivent un ordre précis : `agentbus status <agent> <state> <message>`. Le message se place toujours en dernier.
4. **Nommage normalisé** : Les noms de projets et d'agents doivent respecter l'expression régulière `^[a-z][a-z0-9_-]{0,31}$` (minuscules, débutant par une lettre).
