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

### Étape 1 : Prérequis Système

Vérifiez la présence des outils suivants :
- **Go** (version 1.22 ou supérieure)
- **Docker** et **Docker Compose**
- **Python** (version 3.11 ou supérieure)
- **Git**, **curl**, **make**

### Étape 2 : Installer `herdr`

`herdr` est le gestionnaire de multiplexage et d'espaces de travail terminal pour agents IA.

1. Installez le binaire officiel :
   ```bash
   curl -fsSL https://herdr.dev/install.sh | sh
   ```
2. Vérifiez que `herdr` est accessible dans votre `PATH` :
   ```bash
   herdr --version
   ```

### Étape 3 : Installer et Compiler le Plugin `herdr-plus`

`herdr-plus` gère le sélecteur flou de projets et l'ouverture automatisée des espaces de travail multi-onglets.

1. Clonez le dépôt `herdr-plus` dans votre dossier d'outils local :
   ```bash
   mkdir -p ~/Tools/herdr-plugins
   git clone https://github.com/cloudmanic/herdr-plus.git ~/Tools/herdr-plugins/herdr-plus
   ```

2. Compilez le binaire du plugin :
   ```bash
   cd ~/Tools/herdr-plugins/herdr-plus
   make build
   ```
   *Vérifiez la présence de l'exécutable généré dans `~/Tools/herdr-plugins/herdr-plus/bin/herdr-plus`.*

3. Configurez le raccourci dans `~/.config/herdr/config.toml` :
   ```toml
   # Sélecteur de projets herdr-plus : Préfixe (Ctrl+B) puis Flèche Haut
   [[keys.command]]
   key = "prefix+up"
   type = "plugin_action"
   command = "cloudmanic.herdr-plus.projects"
   description = "herdr-plus: projects"
   ```

### Étape 4 : Compiler et Installer `agent-bus-monitor`

1. Placez-vous à la racine du dépôt `agent-bus-monitor` :
   ```bash
   cd /data/projects/agent-bus-monitor
   ```

2. Compilez et installez les outils Go dans votre `PATH` :
   ```bash
   go build -o busmon ./cmd/busmon
   go build -o agentbus ./cmd/agentbus
   go install ./...
   ```

3. Liez les compétences (skills) des agents pour vos clients IA :
   ```bash
   scripts/link-role-skills.sh
   ```

4. Lancez le broker Redis local :
   ```bash
   docker compose up -d
   docker compose ps
   # Vérifiez que redis:8-alpine tourne sur 127.0.0.1:6380
   ```

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

Supposons un projet applicatif situé dans `/data/projects/mon-app`.

1. **Créer et attacher la session herdr du projet** :
   *Note : herdr enregistre les plugins par session active. La session doit être démarrée pour que la liaison prenne effet.*
   ```bash
   herdr --session mon-app
   ```

2. **Provisionner le projet avec `bootstrap`** (depuis un terminal au sein de la session herdr) :
   ```bash
   /data/projects/agent-bus-monitor/scripts/bootstrap new mon-app /data/projects/mon-app
   ```
   Cette commande :
   - Vérifie que le broker Redis est opérationnel.
   - Enregistre `herdr-plus` dans la session active.
   - Génère le modèle d'espace de travail dans `~/.config/herdr-plus/projects/mon-app.toml`.
   - *(Options facultatives : `--index` pour indexer le code, `--cron` pour le rapport quotidien).*

3. **Ouvrir l'Espace de Travail de l'Équipe** :
   - Appuyez sur `Ctrl+b` puis `Flèche Haut` (ou exécutez `herdr plugin action invoke cloudmanic.herdr-plus.projects`).
   - Sélectionnez `mon-app`.
   - `herdr` ouvre automatiquement les onglets de l'équipe :
     - `master` : agent coordinateur
     - `coder` : agent de développement
     - `foureyes` : agent de revue critique
     - `sentinel` : agent de veille et de santé
     - `busmon` : interface TUI de supervision en direct
     - `dispatch` : démon de réveil automatique

4. **Assigner la première tâche** :
   - Rendez-vous sur l'onglet `busmon`.
   - Dans le champ `INPUT` en bas, tapez :
     ```text
     @master Initialise la structure du projet et prépare le plan de travail.
     ```
   - Le coordinateur prend la directive, met à jour son statut sur le bus, crée les sous-tâches sur le `board` et mandate `coder` via `agentbus cmd coder ...`.

---

### Cas B : Reprendre un Projet EXISTANT (`recall`)

1. **Ouvrir la session herdr du projet** :
   ```bash
   herdr --session mon-app
   ```

2. **Rappeler le projet** :
   ```bash
   /data/projects/agent-bus-monitor/scripts/bootstrap recall mon-app
   # ou simplement : /data/projects/agent-bus-monitor/scripts/bootstrap mon-app
   ```

3. **Rouvrir les onglets** via le sélecteur `Projects` (`Ctrl+b` puis `Haut`).
   > **Important** : Le rappel rouvre les onglets avec de nouvelles sessions d'agents fraîches. Pour reprendre une conversation précédente dans un client tel que Claude Code, utilisez la reprise native :
   > ```bash
   > claude --resume  # puis choisir mon-app:master ou mon-app:coder
   > ```

---

## 6. Synthèse des Commandes Essentielles

| Action | Commande |
|---|---|
| Afficher le dashboard live | `busmon --project mon-app` |
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
