<p align="center"><img src="docs/assets/agent-bus-monitor-logo.png" alt="agent-bus : des agents IA sur un même bus Redis, qui se passent des messages le long d'un flux partagé" width="900"></p>

# agent-bus

[English](README.md) · **Français**

Un bus de coordination multi-agents autonome sur **Redis Streams**, et l'outillage Go qui
l'entoure. Les agents publient états, commandes et notifications sur une instance Redis
partagée, dans un espace de noms de projet obligatoire. `busmon`, un tableau de bord en
terminal, montre ce trafic en direct. Le broker, le client et le moniteur vivent tous ici ;
rien ne dépend d'un autre projet.

Il se veut **petit, simple et rapide** : un conteneur Redis, deux binaires Go et quelques
scripts, sur votre propre machine. Il est aussi **léger pour vos abonnements LLM** : rien
n'interroge un agent en boucle, un agent au repos attend sur le bus sans dépenser de jetons, et
le dispatcher ne tape dans son volet qu'une ligne de réveil quand du travail l'attend (plus,
si vous l'activez, un `/clear` après la passation déclarée par l'agent lui-même). Les fenêtres
d'usage et le remplissage du contexte sont lus dans des sources locales, sans dépenser de jetons.

<p align="center"><img src="docs/assets/busmon.png" alt="busmon sur le projet fictif acme-shop : cinq agents, le fil d'activité, le tableau des tâches et les demandes suivies" width="900"></p>

<p align="center"><sub>busmon sur un projet fictif (<code>acme-shop</code>), généré par <code>scripts/busmon-demo.sh</code>.</sub></p>

**Ce que vous obtenez**

- **Un bus partagé** : états, rapports, directives et notifications par projet, avec une
  livraison au moins une fois à chaque agent (`agentbus subscribe`).
- **Un tableau de bord en direct** : qui travaille, qui est bloqué ou attend une personne ; le
  tableau des tâches suivies ; les demandes encore sans réponse (`busmon`).
- **Des réveils sans scrutation** : un dispatcher réveille le volet d'un agent au repos quand du
  travail l'attend, et escalade quand personne ne répond (`agentbus dispatch`).
- **Une équipe en une commande** : rôles master, coder, relecteur et sentinel, lancés pour
  Claude Code, Codex et d'autres depuis un seul manifeste (`agentbus up`, `roles.toml`).
- **Les grands changements en missions** : un conducteur fusionne, teste et fait relire le
  travail des agents (`agentbus missions`).
- **Les décisions vous sont posées en questions** : un agent demande, busmon montre les options,
  votre réponse le réveille (`agentbus ask`).
- **Les quotas surveillés pour vous** : les fenêtres d'usage de chaque fournisseur sont lues
  localement ; les agents sont retenus avant la limite et le projet se met en pause jusqu'à la
  réinitialisation (`agentbus quota`, `agentbus pause`), et un rôle peut passer sur un profil de
  repli (`agentbus swap`).
- **Une équipe qui survit à son contexte** : à 80 % de contexte, un agent passe la main ;
  sentinel recharge un agent après une mise à jour de son client (`agentbus handoff`,
  `agentbus reload`).
- **Santé et consommation** : la santé mesurée du projet (`agentbus health`, F5) et les jetons
  par projet, agent et jour, jamais un coût (`agentbus tokens`, `U`).

## Démarrage rapide

Il vous faut Linux ou macOS, Go (la version de `go.mod`), Docker avec Compose, Git et curl.
herdr (0.9.0 ou plus récent) est installé pour vous avec `--install-herdr`. Il faut aussi au
moins un client d'agent, que vous installez vous-même : Claude Code (`claude`), Codex CLI
(`codex`), Kimi Code CLI (`kimi`) ou Grok Build (`grok`).

```bash
git clone https://github.com/netbja/agent-bus-monitor && cd agent-bus-monitor
./setup.sh install --install-herdr     # outils, binaires, compétences, plugin herdr, Redis
agentbus statusline --print-install    # pour Claude Code : le bloc statusLine de ~/.claude/settings.json
herdr --session myproject              # puis, dans herdr :
agentbus up myproject ~/code/myproject --as <votre-nom>
```

> **Vu dans la liste des plugins de herdr ?** Installez-le avec les commandes ci-dessus, pas
> avec `herdr plugin install` : le plugin a besoin du broker Redis, des binaires et des
> compétences que `./setup.sh` met en place, et setup le relie à herdr pour vous.
> `herdr plugin install netbja/agent-bus-monitor` s'arrête sur le même conseil et n'installe rien.

`agentbus up` prépare l'équipe et ouvre son espace de travail, busmon en premier. La visite
guidée de busmon, au premier lancement, présente son écran (F1 puis `t` la rejoue). Ensuite,
`Ctrl+B` puis `↑` (le sélecteur Projects du plugin) rouvre n'importe quel projet.

- `./setup.sh update` tient la machine à jour ; busmon affiche `↑ vX.Y.Z` dans son en-tête quand
  une version plus récente existe.
- `./setup.sh status` montre ce qui est installé.
- `./setup.sh uninstall` le retire, en demandant à chaque étape.

L'étape `statusLine` donne à agent-bus les limites de Claude Code et le remplissage du contexte
de chaque session : sans elle, les jauges Claude et l'alerte de contexte n'ont rien à lire.
Codex, Grok, Kimi Code et agy n'ont besoin de rien.

### Premier lancement

Ce qui vous attend la première fois :

- **Un nom de projet est en minuscules** : une lettre, puis des lettres, chiffres, `-` ou `_`
  (32 au plus). `agentbus up MyProject …` est refusé avec la règle et la commande en minuscules à
  lancer à la place.
- **Le dialogue de confiance du client.** Quand Claude Code ou Codex n'a jamais ouvert le dossier
  du projet, `up` démarre seul le premier agent de ce client et nomme le volet où son dialogue
  attend votre réponse ; les autres agents de ce client démarrent une fois que vous avez répondu.
- **busmon a besoin d'un projet.** Lancé sans, il dit de passer `--project <p>` et renvoie vers
  `busmon --list` (les projets du broker) et `busmon --pick` (le sélecteur).
- **Une équipe vide le dit.** `agentbus agents` sur un projet qu'aucun agent n'a encore rejoint
  le dit, au lieu de ne rien afficher ; les agents apparaissent à leur démarrage.
- **Qui vous êtes sur le bus.** Dans busmon, vous écrivez en tant que personne : `--as <votre-nom>`
  (ou `AGENT_BUS_HUMAN`, ou `agentbus identity set <votre-nom>`). Depuis un shell, `agentbus`
  écrit au nom de `AGENT_BUS_AGENT`, et de `anonymous` quand elle n'est pas définie ; dans un
  volet herdr où elle ne l'est pas, une commande qui écrit est refusée avec le correctif, pour
  qu'un agent ne publie jamais sous un mauvais nom.

**Pull requests et forges.** Le bus lui-même n'a besoin que de Git : le conducteur des missions
fusionne et teste sur des branches locales et ne pousse jamais. La livraison vers une forge passe
aujourd'hui par GitHub : l'agent docs ouvre sa pull request avec la CLI `gh`. GitLab, Bitbucket et
un mode local sans forge sont prévus pour la v0.26.

**Nouveautés :** voir le [CHANGELOG](CHANGELOG.md) (en anglais), et [`docs/releases/`](docs/releases/)
pour les notes complètes de chaque version. Le guide pas à pas existe en français :
[Guide opérationnel](docs/OPERATIONAL-GUIDE.fr.md) ; sa section 6 montre comment lancer une
petite tâche et un changement sur plusieurs jours. La référence complète (composants, broker et
mot de passe, installeur, commandes, conventions du bus, connexion distante) est dans le
[README anglais](README.md).

## busmon en bref

busmon est l'écran de la personne : ce qui vous attend, qui fait quoi, et ce qui s'est dit.

- **En-tête** : le projet, le lien de busmon lui-même vers Redis (`● bus ok`, ou `▲ bus down`),
  le dispatcher et le conducteur, ce qui est pour vous (`♙ 4 for you`, `● 2 unread`), la santé du
  projet, une pause éventuelle, votre nom, une jauge d'usage par fournisseur, l'heure.
- **For you** : ce que vous seul pouvez trancher : les questions ouvertes d'un agent (`ask`), un
  agent qui attend votre décision (`wait`), une revue à 4 yeux qui vous concerne (`gate`), les
  messages qui vous sont adressés et que vous n'avez pas ouverts (`unread`). Entrée en ouvre un ;
  `!`, `w` ou F4 répond aux questions (sans question ouverte, `w` ouvre la fenêtre de rédaction
  vers master).
- **Agents** : une ligne par agent, ce qui demande votre attention d'abord ; l'état **déclaré**
  (jamais écrasé par le silence), son âge, la file, le contexte, des indicateurs en toutes
  lettres et le message. Entrée ouvre sa page ; `i` y écrit.
- **Les onglets du panneau du bas** (`1`–`4`, ou `←` `→` quand l'un d'eux a le focus, en boucle
  comme les onglets de herdr) : **Activity** (le fil en direct ; Entrée déplie une ligne, `/`
  filtre, `f` arrête le défilement), **Board** (les tâches), **Requests** (qui doit une réponse à
  qui), **Plan** (le plan du lot).
- **Saisie** : `i` ou `:` pour écrire, `@<agent> <texte>` réveille cet agent, `@all <texte>`
  tous les agents enregistrés ; `I` ouvre la fenêtre de rédaction sur plusieurs lignes (Ctrl+S
  envoie).
- **Pied de page** : les touches de l'endroit où vous êtes (F1 les liste toutes). À son extrémité
  droite, quelques secondes, une ligne discrète dit ce qui s'est passé tout seul pendant que vous
  regardiez : une pause posée par le dispatcher ou un agent (`▲` pour une pause de quota), la
  reprise du projet, le contexte d'un agent vidé après sa passation, un rechargement commencé,
  réussi (`✓`) ou échoué (`▲`), un rôle passé sur un profil de repli ou revenu, une vérification
  de santé qui se dégrade (`▲`) ou se rétablit (`✓`). Une à la fois, 4 s chacune (2 s quand
  d'autres attendent, et au-delà de cinq, le reste en une ligne `N more automatic actions (R:
  recap)`) ; en couleurs 24 bits, elle s'efface en fondu. Elle ne sonne, ne marque et ne compte
  jamais, et ne montre jamais votre propre action, une question ou un message pour vous : ceux-là
  restent dans For you. Ce qui s'est passé pendant que busmon était fermé est dans le récapitulatif
  (`R`).

**Pages** (chacune s'ouvre sur la zone du bas, Échap revient) : Entrée (l'élément en entier),
`!` `w` F4 (les questions), F1 `?` (l'aide ; `t` y rejoue la visite), F2 `r` (les demandes
suivies), F3 `s` (les services et le broker Redis), F5 (la santé du projet), `R` (le
récapitulatif « où en étais-je ? »), `P` (le plan), `U` (les jetons).

À 120 colonnes et plus, For you et Agents côte à côte au-dessus des onglets ; de 80 à 119, l'un
au-dessus de l'autre ; en dessous de 80, une vue à la fois. Chaque état est aussi un glyphe et un
mot : la couleur n'est jamais le seul signal (`NO_COLOR` la coupe).

## Licence

MIT, voir [LICENSE](LICENSE). Copyright (c) 2026 NetBJA.
