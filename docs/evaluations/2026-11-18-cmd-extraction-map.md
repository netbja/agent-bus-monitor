# Cartographie : ce qu'il faut extraire de `cmd/`

Base mesurée : `5a90e4d` (dev, 2026-11-18). Rapport de lecture seule, aucun code modifié.
Objectif : décider ce qui doit quitter `cmd/agentbus` et `cmd/busmon`, dans quel ordre, et ce qui doit rester.

## 1. Constat chiffré

| | Code (hors tests) | Tests | Fichiers |
|---|---|---|---|
| `cmd/agentbus` | 30 684 | 31 903 | 125 |
| `cmd/busmon` | 35 487 | 35 621 | 119 |
| **`cmd/` total** | **66 171** | 67 524 | 244 |
| Module entier | ~126 000 | ~129 000 | |

→ **52 % du code non-test du module est dans deux paquets `main`.**

Ce que l'architecture fait déjà bien : la logique *pure* est presque toujours dans un paquet
(`selfrepair.Decide`, `health/stall.go`, `dispatch.Run`, `bus/`…). Les fichiers de `cmd/`
injectent leurs dépendances (`xxxDeps`) et sont testables. Peu d'appels Redis directs : ils passent par `bus`.

Le problème n'est donc pas la testabilité. Il y en a trois autres :

1. **`cmd/agentbus` contient le runtime complet du service dispatch** : chores, lecteurs des checks santé,
   actions de self-repair, jugement de reboot. C'est du code de démon, embarqué dans le paquet `main` du CLI.
   `dispatch.Config` expose des hooks, et c'est `cmd/agentbus` qui les implémente (`service*()` : ~25 fonctions).
2. **Les opérations d'équipe** (`up`, `swap`, `retire`, `reload`, `suspend`, `resume`) sont dans `main`
   et mélangent logique et affichage (`up.go` : 83 écritures `stdout`/`stderr`/`os.Exit`). Le service en a besoin
   (`serviceEnsureHolder` dans `up.go`, `serviceRelaunch`, `popend` qui rejoue `retire`), et `busmon`
   les déclenche en relançant le binaire `agentbus` (`pick.go`, `sleep.go`, `termpreview.go`).
3. **Duplication entre les deux binaires**, qui ne peuvent pas partager de code puisque ce sont deux `main`.

Coût mesuré (4 vCPU) : `go test ./cmd/busmon` prend **83 s** pour un seul paquet, donc sans parallélisme
inter-paquets. 0 appel `t.Parallel()` ; `TestShellAlignmentSuite` à elle seule : 39 s.
`./cmd/agentbus` : 19 s. Pour comparaison, `./bus` : 2 s, `./dispatch` : 0,04 s.

## 2. `cmd/agentbus` : groupes à extraire

### A. Runtime du service dispatch : 6 722 lignes, 31 fichiers (+ ~190 lignes de `service*` dans `quotacmd.go`)

`healthchecks` 992 · `stallchecks` 584 · `selfrepairchore` 566 · `servicerun` 505 · `mergequeuechore` 369 ·
`selfrepairact` 344 · `notifyfacts` 338 · `reboot` 270 · `notifychore` 228 · `modelcatalogue` 217 ·
`runbudget` 197 · `redislatest` 193 · `popend` 176 · `githubcheck` 172 · `llmcheck` 169 · `diskcheck` 164 ·
`idlemaster` 162 · `backupschedule` 157 · `reviewqueue` 129 · `healthchores` 116 · `holderprobe` 95 ·
`idleimplementer` 87 · `runrecord` 79 · `repocheck` 77 · `backupcheck` 70 · `popdonecheck` 57 ·
`notifycheck` 57 · `holdernotice` 48 · `dispatchhost` 43 · `loginexpiry` 42 · `historyservice` 19

- **Cible** : `internal/dispatchsvc/`, qui construit le `dispatch.Config` complet à partir de `bus.Bus` + `getenv` + chemins.
  `cmd/agentbus` n'appelle plus que `dispatchsvc.Config(...)` puis `dispatch.Run`.
- **Pourquoi** : ces fichiers n'impriment rien (0 `fmt.Fprint`/`os.Exit` dans `healthchecks`, `selfrepairchore`,
  `notifychore`, `mergequeuechore`). Ils sont déjà découplés de la CLI.
  C'est aussi un prérequis de la décision *un dispatcher par hôte* (2026-10-26, décidée mais pas construite) :
  un runtime dans un paquet peut être hébergé ailleurs que dans `agentbus service run`.
- **Garde-fou à ajouter avec l'extraction** : un test d'imports (même principe que `TestNoStructWriterOfTheBoard`) qui
  vérifie que `dispatchsvc` n'importe ni `os/exec` hors d'une liste blanche (git en lecture, `systemctl`, notifieur),
  ni les paquets d'UI. La règle « le dispatcher n'exécute jamais de code du dépôt » devient alors vérifiée
  par la compilation et les tests, et non plus seulement par la revue.
- **Bloquant** : le groupe A appelle 27 fonctions définies dans d'autres fichiers de `cmd/agentbus`.
  Elles doivent sortir **avant** lui :

| Appelé par A | Défini dans | Destination proposée |
|---|---|---|
| `realBackupDeps`, `makeArchive`, `hostProjects`, `backupLastPath` | `backupcmd.go` | `backup/` (existe) |
| `loadModelsConfig`, `modelsCheckReport`, `doctorRows`, `realDoctorDeps` | `modelscmd`, `modelcheck`, `doctor` | `models/` (existe) |
| `manifestPath` | `roles.go` | `rolecfg/` (existe) |
| `realHolderProbe`, `holderEvidence`, `holderCheck`, `holderReady`, `waitHolder` | `holderhealth`, `holdercmd`, `up` | `holder/` ou `termhost/` |
| `missionsOf`, `replaced`, `resumeChoice`, `recoveryHint` | `swap`, `upteam`, `recoveryhint` | groupe B (ci-dessous) |
| `projectDeliveryBase` | `deliverbase.go` | `forge/` (groupe C) |
| `mergeQueueInput` | `mergequeuecmd.go` | `mergequeue/` (existe) |
| `desktopRun`, `desktopNotify`, `notifyStateDir` | `notificationscmd`, `quotacmd` | `notify/` (existe) |
| `collectAgentUsage` | `budget.go` | `usage/` ou `quota/` |
| `endpointFPKey`, `endpointHost` | `peer.go` | `peer/` (existe) |
| `runDuration` | `run.go` | avec `runbudget` dans `dispatchsvc` |

### B. Opérations d'équipe (cycle de vie) : 5 805 lignes, 17 fichiers

`up` 1 313 · `suspend` 657 · `swap` 519 · `start` 470 · `resume` 470 · `reload` 435 · `resumeall` 365 ·
`retire` 315 · `trust` 304 · `start_deduce` 242 · `suspendsnap` 222 · `upteam` 165 · `suspendquiet` 129 ·
`shutdown` 87 · `launchguard` 52 · `pop` 41 · `recoveryhint` 19

- **Cible** : un paquet `team/` (ou l'élargissement de `lifecycle/`, 587 lignes, et de `relaunch/`, 242 lignes,
  qui existent déjà et sont minces). Chaque opération retourne un résultat ou une suite d'événements typés.
  La couche `cmd/` les affiche.
- **Travail réel** : séparer le rendu de la logique dans `up.go` (83 sorties), `swap`, `suspend`.
  C'est l'extraction la plus coûteuse, donc à découper par opération.
- **Reste dans `cmd/`** : `start_ask.go` (342, l'écran interactif) et le parsing des arguments.
- **Gain** : le service et `busmon` appellent la même fonction au lieu de relancer `agentbus` en sous-processus.
  À trancher : garder le sous-processus côté `busmon` peut rester voulu (isolation, binaire installé = vérité).
  Voir Q2.

### C. Livraison et forge : 1 028 lignes, 5 fichiers

`deliver` 756 · `gatecmd` 181 · `verdicthead` 36 · `verdict_notice` 33 · `deliverbase` 22

- **Cible** : `forge/`, qui ne fait que 156 lignes alors que la logique de livraison (preuve 4-eyes liée au head,
  base de livraison) est dans `main`. Petite extraction, isolée, bon candidat pour commencer.

### D. Holder et terminal : 3 373 lignes, 12 fichiers

`livecheck` 413 · `attach` 439 · `attachkeys` 391 · `livecheckproject` 340 · `fakeclient` 330 · `holdercmd` 307 ·
`screencmd` 239 · `hostrelay` 232 · `holderhealth` 221 · `term` 220 · `hostrun` 172 · `holderdrain` 69

- **À extraire** : `attachkeys.go` (machine à états du préfixe, pure) vers `holder/` ou `termhost/` ;
  `holderhealth` (sonde) vers `holder/` (requis par le groupe A) ; `hostrun` (exécution d'une directive hôte
  acceptée) vers `dispatchsvc`, car c'est le dispatcher qui l'exécute.
- **Reste dans `cmd/`** : `attach`, `screencmd`, `term`, `holdercmd` (points d'entrée).
- **À isoler** : `livecheck*` et `fakeclient` (1 083 lignes) sont de l'outillage de test livré dans le binaire
  de production. À déplacer vers `internal/livecheck/` ; éventuellement derrière un build tag. Voir Q3.

### E. Rôles, modèles, identité : 2 324 lignes, 12 fichiers

`doctor` 457 · `reportresume` 324 · `roles` 265 · `modelscmd` 242 · `draftcmd` 229 · `modelcheck` 179 ·
`identityguard` 165 · `identity` 102 · `reviewers` 99 · `ownprofile` 93 · `bind` 85 · `turnhook` 84

- **À extraire** : `modelcheck`, `doctor` (calcul des lignes), `reviewers` vers `models/` ou `rolecfg/` ;
  `identity` + `identityguard` vers un petit `identity/`, car `busmon` a sa propre notion d'expéditeur
  (`human.go`) et les deux doivent rester d'accord.
- **Reste** : `roles.go` (CLI appelée par les scripts), `draftcmd`, `bind`, `turnhook`, `reportresume` (hooks).

### F. Reste dans `cmd/agentbus` (≈ 11 400 lignes) : c'est sa place

Câblage CLI et rendu : `main`, `commands`, `help`, `parse`, `configcmd`, `board`, `budget`, `usage`, `thread`,
`agents`, `verdicts`, `reports*`, `requests*`, `historycmd`, `tokenscmd`, `quotacmd` (partie CLI), `plancmd`,
`plandrift`, `ideacmd`, `askcmd`, `checkback`, `handoff`, `mem`, `peer`, `projects`, `missions`, `notification*`,
`notifysetup`, `backupcmd`/`restorecmd` (partie CLI), `setupcmd`, `servicecmd`, `servicelogin`, `servicestale`,
`supervisecmd`, `healthcmd`, `pause`, `run`, `settingswarn`, `subscribe`, `background`, `dispatchcmd`,
`selfrepaircmd`, `mergequeuecmd`.

## 3. `cmd/busmon` : ce qu'il faut extraire

`busmon` est très majoritairement de l'UI (Elm/bubbletea : `tea_*`, vues, pages, thèmes, widgets) : **elle reste**.
Deux groupes font exception.

### M. Modèle de vue « qui doit quoi à qui » : 2 628 lignes, 11 fichiers, aucune référence UI

`recap_model` 516 · `requests_view` 483 · `tracking` 401 · `work_model` 309 · `tea_foryou` 280 · `unread` 158 ·
`mergequeue` 146 · `projects_counts` 121 · `progress` 106 · `waiting` 62 · `foryou` 46

- 0 référence à `lipgloss`, aux styles ou au thème (sauf 4 dans `tracking`). Ce sont des calculs sur les données du bus.
- **Cible** : `workview/` (ou `bus/view/`).
- **Gain** : `agentbus` calcule aujourd'hui sa propre version des mêmes faits (`notifyfacts.go` : « every event is
  a fact … busmon already shows » ; « For you » apparaît dans 12 fichiers de `cmd/agentbus`). Une seule source
  empêche que la notification et l'écran se contredisent. Les tests de ce modèle sortent aussi de la suite de 83 s.

### M2. Utilitaires génériques : 1 145 lignes, 6 fichiers

`clipimage` 343 · `markdown` 335 · `codexrelease` 183 · `localcache` 114 · `update` 86 · `lineedit` 84

- `update.go` + `codexrelease.go` (vérification des releases GitHub) recoupent `redislatest.go` et
  `latestReleaseCache()` de `cmd/agentbus` : **trois vérifications de release** pour un seul besoin. Cible : `release/`.
- `localcache.writeFileAtomic` : voir §4.

### Suppression de `tcell`

`gdamore/tcell` n'est plus utilisé que comme **type** : `tcell.Style` et `tcell.Color` dans `theme.go` (25 références)
et l'adaptateur `lipFrom`/`lipColor` de `tea_grid.go` (6). C'est un reste de la migration Charm.
Le remplacer par deux types locaux retire une dépendance directe et 3 indirectes (`gdamore/encoding`,
`xo/terminfo`, `lucasb-eyer/go-colorful` s'il n'est plus tiré par lipgloss ; à vérifier avec `go mod why`).

## 4. Duplications à résorber

Fonctions définies avec le même nom dans les deux binaires (et parfois ailleurs) :

| Fonction | Emplacements |
|---|---|
| `humanAge`, `short7`, `shellWord`, `tokCount`, `plainLine`, `sortedAgentNames`, `notifyStateDir` | `cmd/agentbus` + `cmd/busmon` |
| `plural` | 4 : `projfiles`, `backup`, `cmd/agentbus`, `cmd/busmon` |
| `oneLine` | 6 : `plan`, `rolecfg`, `quota`, `health`, `cmd/agentbus`, `cmd/busmon` |
| écriture atomique | 5 : `config.writeAtomic`, `quota.writeAtomic`, `setup.writeAtomic`, `lifecycle.writeAtomic`, `busmon.writeFileAtomic` |

- Les helpers de texte vont dans `internal/textfmt/`.
- **L'écriture atomique a un enjeu de sécurité** : permissions (0600/0700), `fsync` avant le `rename`,
  `fsync` du dossier. Cinq implémentations veut dire cinq comportements possibles sur des fichiers d'état et de
  secrets (`notify.toml`). À unifier dans `internal/fsutil.WriteAtomic`, après comparaison des cinq
  (ne pas présumer qu'elles sont équivalentes).

## 5. Ordre proposé (chaque étape est une PR vers `dev`)

| # | Étape | Lignes déplacées | Risque | Commentaire |
|---|---|---|---|---|
| 0 | `internal/fsutil.WriteAtomic` + `internal/textfmt` | ~300 | faible | Aligner les permissions et le `fsync` ; tests ciblés |
| 1 | Retirer `tcell` de `busmon` | ~60 modifiées | faible | Goldens inchangés = preuve |
| 2 | Groupe C → `forge/` | ~1 000 | faible | Isolé, bon test du schéma |
| 3 | Prérequis du groupe A (tableau §2.A) → paquets existants | ~1 500 | moyen | Lève les 27 dépendances croisées |
| 4 | Groupe A → `internal/dispatchsvc` + test d'imports | ~6 900 | moyen | Le plus gros gain structurel |
| 5 | Groupe M → `workview/`, puis `notifyfacts` s'appuie dessus | ~2 600 | moyen | Supprime la double vérité busmon/notifications |
| 6 | Groupe B, opération par opération (`retire` → `reload` → `swap` → `up` → `suspend`/`resume`) | ~5 500 | élevé | Séparer le rendu ; une PR par opération |
| 7 | D et E (reste) ; `livecheck` → `internal/livecheck` | ~2 500 | faible | |
| — | Hors extraction : paralléliser les suites lentes de `busmon` | — | faible | `TestShellAlignmentSuite` (39 s), `TestRowHintEveryRowAndItsOutcome` (9 s) |

**Résultat attendu** : `cmd/agentbus` passe d'environ 30 700 à environ 11 500 lignes, `cmd/busmon` d'environ 35 500 à environ 31 500.
`cmd/` passe ainsi d'environ 52 % à environ 34 % du code non-test.

Points d'attention transverses :

- **Fichiers chauds** (`open-questions/2026-11-14-hot-files.md`) : les étapes 3, 4 et 6 touchent `up.go`, `servicecmd.go`
  et `quotacmd.go`. Ne pas les mener en parallèle d'autres chantiers sur ces fichiers.
- **Règles CLAUDE.md** : l'extraction ne change ni le protocole (`bus/`), ni les écritures fencées, ni le registre
  `config/keys.go`. Une PR d'extraction ne doit contenir **aucun changement de comportement** :
  un déplacement, des tests identiques et verts.
- `TestNoStructWriterOfTheBoard` et le test des littéraux d'env du registre doivent couvrir les nouveaux paquets.
  Vérifier qu'ils parcourent tout le module et pas seulement `cmd/`.

## 6. Questions pour la personne

- **Q1** : faut-il préparer dès l'étape 4 un binaire dédié au dispatcher d'hôte (décision 2026-10-26), ou
  rester sur `agentbus service run` ?
- **Q2** : `busmon` doit-il continuer à lancer `agentbus` en sous-processus pour `up`, `suspend` et `attach`
  (isolation, binaire installé = source de vérité), ou appeler le paquet `team/` directement ?
- **Q3** : faut-il sortir `livecheck`/`fake-client` du binaire de production (build tag), au prix d'un second
  artefact pour la vérification avant release ?
- **Q4** : faut-il viser une taille maximale de fichier (par exemple 800 lignes) comme règle de revue, ou laisser
  ce point à la question des fichiers chauds ?

## Méthode

Classement à partir de l'en-tête de chaque fichier ; mesure des appels Redis, `exec`, sorties et références UI
par fichier ; recherche des fonctions de même nom dans les deux binaires ; graphe des appels entre le groupe A
et le reste de `cmd/agentbus` ; temps de `go test -count=1 -json` sur `cmd/busmon`. Les tailles sont des `wc -l`
(commentaires inclus). Les estimations du §5 sont indicatives.
