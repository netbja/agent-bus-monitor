# Guide opérationnel : faire tourner une équipe avec agent-bus

[English](OPERATIONAL-GUIDE.md) · **Français**

Ce guide s'adresse à la personne qui fait tourner l'équipe : démarrer un projet, lui donner du
travail, le regarder, fusionner ce qu'il livre, mettre les outils à jour et l'arrêter. Le
[README](../README.fr.md) couvre l'installation et ce qu'est chaque pièce. `agentbus help <command>`
donne les options exactes de n'importe quelle commande.

Dans les exemples, le projet est `myapp`. `-p myapp` est le raccourci de `--project myapp` ; vous
pouvez à la place faire `export AGENT_BUS_PROJECT=myapp`.

## 1. Vue d'ensemble

- **Votre machine fait tourner, par projet :**
  - **le holder**, un service utilisateur qui héberge le terminal de chaque agent ;
  - **le dispatcher**, un service qui réveille les agents quand du travail les attend, escalade les
    silences, relance les agents morts et surveille quotas, contexte et santé ;
  - **le conducteur de missions**, un service qui mène les grands changements.
- **Les agents** (master, coder, foureyes, sentinel, et des rôles à la demande) se parlent par le bus
  Redis.
- **Ce qu'ils ne font jamais :**
  - rien ne tape dans le terminal d'un agent, hormis la ligne de réveil du dispatcher (et `/clear`
    après une passation déclarée) ;
  - le dispatcher n'exécute jamais de code du dépôt.
- **Vous** regardez dans `busmon`, répondez dans busmon, et entrez dans le terminal d'un agent avec
  `t` (busmon) ou `agentbus attach`.
- **Fermer busmon** ou votre terminal n'arrête rien : les services et les agents continuent.

## 2. Démarrer un projet

Depuis le dossier du projet :

```bash
agentbus start              # déduit, montre, ne demande que ce qui manque, puis démarre et ouvre busmon
agentbus start --dry-run    # le plan seulement
agentbus start --yes        # prend les valeurs déduites
```

`start` déduit le nom du projet à partir du dossier, votre nom, et l'équipe, qui est celle des rôles
de démarrage ou `--roles master,coder,foureyes:review`. Il lance ensuite `agentbus up myapp <path>` et
ouvre busmon.

`agentbus up myapp ~/code/myapp` fait la même chose sans la déduction, et sans ouvrir busmon.
Ses options utiles :

- `--as <your-name>` ;
- `--resume` reprend les sessions des agents ;
- `--escalate-to <agent>` nomme qui reçoit les escalades (master par défaut) ;
- `--reload on|propose|off` ;
- `--dry-run`.

**À quoi s'attendre la première fois :**

- **Le dialogue de confiance du client.** Quand Claude Code ou Codex n'a jamais ouvert le dossier,
  `up` démarre seul le premier agent de ce client et dit quel agent attend sur son dialogue de
  confiance. Attachez-vous à lui (`t` dans busmon), répondez, et ses autres agents démarrent.
- **busmon a besoin d'un projet.** Lancez `busmon -p myapp`. `busmon --list` liste les projets du
  broker, et `busmon --pick` les modèles.
- **Qui vous êtes.** busmon écrit en votre nom (`--as`, `AGENT_BUS_HUMAN`, ou `agentbus identity set
  <name>`).

**Joindre un agent :**

```bash
agentbus -p myapp attach master            # F12 puis d revient ; F12 F12 envoie un F12
agentbus -p myapp attach coder --observe   # regarder seulement
agentbus -p myapp screen coder --lines 20  # son dernier écran, en lecture seule
```

La touche de préfixe est `attach.prefix` (`f12` par défaut). Les rôles Kimi et agy demandent
`--prefix <key>`, avec une touche que le client laisse passer : `f1`-`f12`, `ctrl+<letter>`,
`ctrl+\` ou `ctrl+]`.

**Agrandir ou réduire l'équipe.** master fait apparaître un rôle quand le travail l'exige. Vous
pouvez le faire aussi :

- `agentbus -p myapp pop architect` ajoute un rôle, et `--profile <p>` choisit l'un de ses profils ;
- `agentbus -p myapp retire architect` le retire ;
- un rôle à la demande terminé est fermé par le dispatcher lui-même.

## 3. Donner du travail à l'équipe

| | Petite tâche | Grand changement |
|---|---|---|
| Taille | d'une heure à un jour ; une fonctionnalité ou un correctif | plusieurs jours ; plusieurs agents en parallèle |
| Qui pilote | master, dans sa conversation | le conducteur, à partir d'un plan dans Git |
| Ce que vous faites | écrire la demande, répondre aux questions, fusionner | approuver le plan, trancher les escalades, fusionner |

### Une petite tâche : demander à master

1. **Écrivez la demande dans busmon** (`i`), avec le but et la façon dont vous jugerez que c'est fait :
   ```text
   @master Add CSV export to the reports page. Done when: an Export button downloads the
   current filter as CSV, with tests, reviewed, delivered as a PR.
   ```
   Vous pouvez aussi vous attacher à master et taper là.
2. **master répartit.** coder prend la tâche, travaille test d'abord sur sa propre branche, puis défie
   deux relecteurs.
3. **Regardez** le tableau et l'activité dans busmon. Quand un agent a besoin d'une décision, master
   vous la pose sous forme de question à laquelle vous répondez dans busmon (*For you*).
4. **Livraison.** Quand les deux relecteurs ont approuvé le commit exact, master le livre, et la PR
   apparaît dans votre file de fusion (section 5).

### Un grand changement : les missions

Un changement qui prend des jours ne doit pas dépendre d'une seule conversation : son plan vit donc
dans Git :

1. **Demandez un plan, pas du code :**
   ```text
   @master Plan "auth-v2" as missions: docs/changes/auth-v2/brief.md and missions.json on a
   branch, committed; send me the commit. Do not start it.
   ```
   Pour un changement riche en conception, master fait d'abord apparaître l'architecte.
2. **Relisez le plan.** Chaque mission nomme :
   - son `agent` et son `reviewer` (jamais le même) ;
   - ses `depends_on` ;
   - les `allowed_paths` qu'elle peut toucher ;
   - son `test` ;
   - un `brief`.

   Le changement a son propre `test`, et éventuellement `"docs": "docs"` pour que l'agent docs mette à
   jour la documentation à la fin.
3. **Chargez-le** au commit relu :
   ```bash
   agentbus -p myapp mission load docs/changes/auth-v2/missions.json@<commit>
   ```
   Le conducteur travaille alors mission par mission :
   - il envoie chaque mission quand ses dépendances sont intégrées, les indépendantes en parallèle ;
   - il vérifie chaque livraison (chemins autorisés, tests au vert) ;
   - il ouvre la relecture ;
   - il fusionne la mission approuvée dans `change/auth-v2`, jamais dans votre base ;
   - il lance la suite de tests du changement.
4. **Suivez-le :** `agentbus -p myapp mission status auth-v2`, `mission show auth-v2/t3`.
5. **Une mission bloquée** (tests en échec, relecture rejetée, trop de tentatives) va à master, qui
   tranche ou vous demande :
   - `mission retry <change>/<id>` ;
   - `mission reassign <change>/<id> <agent>` ;
   - `mission skip <change>/<id> --reason …` ;
   - `mission abort <change> --reason …`.

   Pour changer le plan, validez un nouveau `missions.json` et chargez-le de nouveau. Les missions
   inchangées gardent leur état.
6. **La fin.** master reçoit un avis `complete`, et livre `change/auth-v2` une fois relu.

## 4. Regarder l'équipe

**busmon** (`busmon -p myapp`) :

- **L'en-tête** montre les services. `waiting-holder` veut dire que le dispatcher attend le holder, et
  `holder-behind` que des agents tournent encore sur une version plus ancienne (section 7).
- ***For you*** liste ce qui vous attend : questions, décisions, PR à fusionner, avis.
- **Les agents** montrent l'état de chacun :
  - `working` ;
  - `idle` ;
  - `blocked` ;
  - `waiting`, c'est-à-dire qu'une personne doit trancher ;
  - `done`.

  Son remplissage de contexte et son quota s'affichent à côté.
- **L'onglet Plan** (`4` ; `6` sous 80 colonnes) montre le plan chargé comme une feuille de route :
  NOW, APPROVED, NEXT par priorité puis par ordre du fichier, LATER (bloqué, puis basse priorité) et
  DONE, replié jusqu'à `d`. Entrée ouvre une tâche ; `p` ou `P` ouvre la même feuille de route en
  tiroir.
- **Touches :**
  - `F3` les services et les holders, avec leurs journaux (Entrée) ;
  - `F5` la santé mesurée ;
  - `M` tous les projets de cette machine ;
  - `U` les jetons ;
  - `H` l'historique ;
  - `F1` toutes les touches.

**Depuis un shell :**

```bash
agentbus -p myapp agents                  # états déclarés
agentbus -p myapp service status          # dispatch et missions : état, dernier enregistrement, chemin du journal
agentbus -p myapp service logs dispatch -n 100
agentbus -p myapp health                  # santé mesurée et ses notes
agentbus quota                            # les fenêtres d'usage de chaque fournisseur
agentbus -p myapp merge-queue             # ce qui attend votre fusion
```

**Quotas.** Le dispatcher lit les fenêtres d'usage localement :

- près de la limite (`dispatch.quota_warn`, 85 %), les agents qui tiennent du travail sont priés de
  finir leur étape ;
- à `dispatch.quota_hold` (95 %), les agents de ce fournisseur sont retenus jusqu'à la
  réinitialisation de la fenêtre ;
- `agentbus swap <role> --profile <fallback>` fait passer un rôle sur un autre profil, et `--back` le
  ramène.

**Pause.** `agentbus -p myapp pause --for 2h` (ou `--until 18:00`, `--until-reset`) ne réveille
personne d'ici là, et `agentbus -p myapp resume` y met fin plus tôt.

**Un agent coincé :**

1. Lisez son écran (`agentbus screen <agent>`), ou attachez-vous.
2. S'il est bloqué sur une invite du client, répondez-y là.
3. Si son processus est mort, le dispatcher le relance dans un nouveau terminal (auto-réparation, au
   plus `dispatch.self_repair_per_day` par jour). `agentbus -p myapp self-repair reset <agent> --as
   <you>` efface une boucle de plantages.

## 5. Relecture, livraison et fusion

- **4 yeux.** Chaque changement est relu par deux relecteurs autres que son auteur. Un verdict nomme
  le commit exact qu'il a lu (`agentbus verdict --head <sha>`) ; un push après une approbation demande
  une nouvelle approbation. `agentbus -p myapp verdicts --subject <s>` montre l'état.
- **Livraison.** `agentbus deliver <branch>` appartient à master et à l'agent docs. Il pousse la tête
  approuvée et ouvre la PR :
  - avec `gh` sur GitHub, `glab` sur GitLab ;
  - avec la page à ouvrir quand la CLI manque, et sur Bitbucket ;
  - en mode local, sans `origin`, il vous donne le `git merge` à lancer.
- **La base.** La PR va vers le `[delivery] base` du modèle de projet (dans
  `~/.config/agentbus/projects/<project>.toml`), sinon `delivery.base` (`main` par défaut).
  - `agentbus config --project myapp` montre lequel s'applique.
  - Après avoir changé le modèle, lancez `agentbus -p myapp service restart missions`.
- **Fusionner vous revient.** La file de fusion (`agentbus merge-queue`, et *For you* dans busmon)
  liste chaque PR sur une tête approuvée, avec ses relecteurs. Vous fusionnez sur la forge.
- **Promotions.** Avec une base `dev`, `main` ne bouge que par l'avance rapide de master vers une tête
  de `dev` publiée, et seulement sur votre accord.
- **La boucle de CI** (GitHub avec `gh`) :
  - quand les vérifications d'une PR sont terminées et que l'une a échoué, l'auteur est réveillé avec
    les vérifications en échec et leurs liens ;
  - après `ci.max_handoffs` passations (3 par défaut), la boucle s'arrête pour cette PR, et vous
    recevez un avis ;
  - `agentbus -p myapp ci reset <pr>` donne plus d'essais à l'auteur, et n'appartient qu'à vous ;
  - une vérification qui ne finit jamais garde une PR silencieuse : le silence n'est pas « vert » ;
  - `ci.loop = false` désactive la boucle.
- **Le budget de run** est désactivé par défaut. Réglez `run.max_duration` (par ex. `8h`) ou
  `run.max_deliveries`.
  - À la limite, le run se vide : master est prévenu, les agents ne peuvent plus envoyer de nouvelles
    demandes, et vous recevez un avis.
  - Il s'arrête quand plus rien n'est en cours, ou après `run.drain_max` (2h), et le projet se met en
    pause.
  - `agentbus -p myapp run` montre le run, et `agentbus -p myapp run reset` en démarre un nouveau.

## 6. Une équipe qui tourne pendant des jours

- **Contexte.**
  - À `dispatch.context_alert` (80 %, Claude Code seulement), un agent écrit un rapport de passation et
    le déclare (`agentbus handoff`).
  - Le dispatcher tape alors `/clear` (`dispatch.auto_clear`, activé par défaut), et l'agent repart de
    son rapport.
- **Recharger après une mise à jour du client.** `agentbus -p myapp reload <role>` redémarre un agent
  au repos sur son rôle, en reprenant sa session ; `--fresh` en démarre une neuve. sentinel le propose
  ou le fait, selon `--reload on|propose|off`.
- **Notifications sur votre téléphone ou votre bureau.** Configurez-les avec `agentbus notifications
  setup`, puis `agentbus notifications test`. Les cibles vivent dans `notify.toml`, et `notify.*` choisit
  quels événements sont envoyés.
- **Sauvegardes.** `agentbus backup` écrit une archive de tous les projets de cette machine : clés
  Redis, réglages, modèles, rôles et historique, les secrets étant laissés de côté et listés.
  - `backup.schedule` la rend automatique.
  - `agentbus restore <archive>` la remet en place. Il refuse un projet vivant.
- **Historique.** `agentbus -p myapp history` relit le passé, `history.keep_days` et `history.max_mb`
  bornent l'archive, et `H` dans busmon l'affiche.
- **Après un redémarrage.**
  - Avec `services.start_at_login` activé, les services et le holder démarrent à l'ouverture de session.
  - Le dispatcher juge alors l'arrêt. Après un arrêt imprévu, il relance les agents sur leurs sessions,
    quand `services.unplanned_restart` le permet, que le dépôt est intact, et dans la limite de
    `services.unplanned_max` (3 relances en 24 h). Sinon il suspend le projet.
  - Vous êtes prévenu une fois par démarrage, et `agentbus -p myapp resume` ramène un projet suspendu.
  - Sans cela, `agentbus up myapp --resume` ramène l'équipe.
- **Avant un redémarrage prévu :**
  - `agentbus suspend --reason "OS updates"` met en pause chaque projet, laisse finir les tours en
    cours, enregistre un instantané et arrête les services. `--project <p>` suspend un seul projet.
  - Après le redémarrage, `agentbus resume --all` (ou `agentbus -p myapp resume`) ramène les services,
    puis les agents sur leurs sessions, puis lève la pause.

## 7. Mettre à jour les outils

`agentbus update` installe la dernière version publiée, avec un instantané pour revenir en arrière
(`--check` affiche le plan, `--rollback` restaure, `--list` montre les instantanés). Elle refuse un
build `dev`, qu'on met à jour depuis son clone. Son mode automatique n'est pas construit. Le chemin
depuis un clone :

1. **Mettre à jour et installer :**
   ```bash
   cd ~/agent-bus-monitor && ./setup.sh update
   ```
   Il tire avec `--ff-only` et recompile. Il liste ensuite les services dispatch et missions de cette
   machine encore sur l'ancienne version, et les redémarre sur votre accord. Il ne touche jamais aux
   holders ni aux agents.
2. **Le nouveau holder.** Sur sa nouvelle version, le dispatcher démarre le holder de cette version.
   Les agents restent sur l'ancien holder jusqu'à ce que vous les déplaciez, et l'en-tête de busmon
   dit `holder-behind`.
3. **Déplacez les agents**, un par un, chacun repris sur sa session :
   ```bash
   agentbus holder drain myapp
   ```
   Il ne déplace que les agents au repos. Le code de sortie 3 veut dire que certains ont été sautés
   (occupés, ou avec un travail d'arrière-plan en cours) : relancez-le plus tard.
   `--ignore-background` déplace un agent dont le travail d'arrière-plan tourne ou est inconnu, et
   n'appartient qu'à vous.
4. **L'ancien holder part.** Quand chaque agent est sur la nouvelle version, `holder drain` retire
   lui-même le holder de l'ancienne version. `agentbus -p myapp service gc` fait de même à la main, et
   la vérification `holder-leftovers` de la santé y renvoie.
5. **Rouvrez busmon** pour qu'il lance le nouveau binaire.

`agentbus -p myapp service restart holder --kill-agents` redémarre le holder d'un coup et tue chaque
agent qu'il héberge. Il est refusé sans l'option. `agentbus up myapp --resume` ramène ensuite les
agents.

## 8. Arrêter

| Quoi | Commande |
|---|---|
| Personne n'est réveillé pendant un temps | `agentbus -p myapp pause --for 2h` / `resume` |
| Tous les projets endormis, à reprendre | `agentbus suspend` / `agentbus resume --all` |
| Arrêter les services, le holder et les agents d'un projet | `agentbus down myapp` (ne supprime rien ; `agentbus up myapp --resume` le ramène) |
| Dire aux agents que le travail est fini | `agentbus -p myapp shutdown` (refusé tant que du travail est en cours) |
| Retirer les services d'un projet | `agentbus -p myapp service remove` |
| Supprimer les clés de bus d'un projet | `busmon --delete myapp` (seulement endormi ; sauvegarde, puis demande) |
| Retirer agent-bus de la machine | `./setup.sh uninstall` (demande à chaque étape) |

## 9. Pièges

1. **Le projet est obligatoire.** Passez `-p <p>` ou exportez `AGENT_BUS_PROJECT`. Les noms sont en
   minuscules : une lettre, puis des lettres, chiffres, `-` ou `_`, 32 au plus.
2. **Les arguments ont un ordre.** Dans `agentbus status <agent> <state> <message>`, le message vient en
   dernier. `--ref abc`, `--ref=abc` et `-r abc` marchent, alors que `-ref abc` et les options courtes
   groupées non.
3. **Un `AGENT_BUS_*` exporté reste avec les services.** `agentbus up` les copie dans le fichier
   d'environnement du projet. En retirer un dans votre shell ne change rien avant le prochain `up` sans
   lui.
4. **Un terminal attaché est celui de l'agent.** Ce que vous y tapez va au client de l'agent. Lancez les
   commandes `agentbus` depuis votre propre shell.
5. **Le silence de la boucle de CI n'est pas du vert.** La boucle ne lit que GitHub, et n'agit sur une
   PR qu'une fois toutes ses vérifications terminées.
6. **Un GitLab auto-hébergé** dont le nom d'hôte ne commence pas par `gitlab.` est lu comme sans forge.
   Nommez-le ainsi, ou réglez `delivery.forge`, qui s'applique à tous les projets de la machine.

## 10. Les commandes d'un coup d'œil

| Tâche | Commande |
|---|---|
| Démarrer, arrêter | `agentbus start`, `agentbus up <p> [--resume]`, `agentbus down <p>` |
| Agents | `agentbus -p <p> agents`, `attach <a>`, `screen <a>`, `pop <role>`, `retire <role>`, `reload <role>`, `swap <role> --profile <f>` |
| Services | `agentbus -p <p> service status`, `service logs <kind> -n 100`, `service restart <kind>`, `agentbus holder drain <p>`, `service gc` |
| Travail | `agentbus -p <p> board`, `request`, `mission load\|status\|show`, `merge-queue`, `ci reset <pr>`, `run` |
| Questions | `agentbus -p <p> asks --open`, `answer` (busmon est plus simple) |
| Regarder | `busmon -p <p>`, `agentbus -p <p> health`, `agentbus quota`, `agentbus tokens`, `agentbus projects` |
| Dormir | `agentbus -p <p> pause`, `resume`, `agentbus suspend`, `agentbus resume --all` |
| Sécurité | `agentbus backup`, `agentbus restore`, `agentbus notifications` |
| Réglages | `agentbus config [--project <p>]`, busmon `C` |
