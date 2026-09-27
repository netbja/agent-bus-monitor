# Rôles indépendants des providers et revue sécurité

Statut : conception de référence, implémentée localement le 2026-09-27.
Le [guide livré](../../AGENT-PROFILES.md) précise les interfaces retenues et les
limites de validation. Aucune activation sur le bus actif n’est attestée.

## Besoins confirmés

- Claude Code, Codex et Kimi CLI sont les trois premiers clients visés.
- Le système doit pouvoir accueillir d'autres clients et providers.
- Un projet peut utiliser un profil commun ou une équipe mixte par rôle.
- Le modèle et, si nécessaire, le niveau de raisonnement sont configurables. Aucun
  effort n’est imposé par défaut (précision utilisateur pendant l’implémentation).
- Les skills restent indépendants du modèle et du provider.
- `security` examine le dépôt et propose des corrections, sans les appliquer
  automatiquement. Il complète `foureyes` sans devenir un second propriétaire
  des mêmes demandes. L'environnement déployé est hors du périmètre initial.

## État constaté

Sources du dépôt à `00fe199` :

- `roles.toml` centralise modèles, permissions, skills et tiers.
- `scripts/agent-launch` construit directement `claude`, avec des options de
  permissions et de fallback propres à ce client. Il retire par défaut
  `ANTHROPIC_API_KEY` pour conserver le comportement de facturation existant.
- `scripts/agent-spawn` délègue au même lanceur que le démarrage normal.
- `scripts/lib/roles.sh` lit le manifeste via Python et `tomllib`.
- `scripts/link-role-skills.sh` cible par défaut le répertoire Claude.
- `roles/foureyes.md` mêle mission de revue, permissions et invocation des skills.
- Le skill sentinel décrit une collecte de consommation fondée sur des sources
  Claude. La disponibilité de cette collecte ne doit pas conditionner le lancement.

Ces constats concernent les sources, pas le binaire installé ni le bus actif.

## Configuration proposée

Conserver `roles.toml` comme manifeste. Ajouter une version de schéma et des
profils d'exécution nommés. Un profil regroupe client, provider, modèle et options
compatibles ; il est sélectionné en bloc pour éviter les héritages incohérents.

Exemple conceptuel : les identifiants de modèles ci-dessous sont des placeholders,
pas des noms de modèles utilisables.

```toml
schema_version = 2

[defaults]
profile = "primary"

[profiles.primary]
client = "claude-code"
provider = "anthropic"
model = "<modele-configure>"

[profiles.deep-review]
client = "codex"
provider = "openai"
model = "<modele-compatible>"

[profiles.kimi]
client = "kimi-cli"
provider = "moonshot"
model = "<modele-configure>"

[roles.coder]
tier = "boot"
skills = ["agent-bus", "tdd", "implement"]

[roles.foureyes]
profile = "deep-review"
tier = "boot"
skills = ["agent-bus", "code-review", "diagnosing-bugs"]

[roles.security]
profile = "deep-review"
tier = "pop"
skills = ["agent-bus", "agent-bus-security"]
```

Un rôle sans `profile` utilise le défaut. Une équipe homogène ne définit aucune
surcharge de profil. Les options natives de permissions et de connexion seront
rangées dans des tables spécifiques au client à l'intérieur du profil ; elles
ne sont jamais interprétées comme des options universelles.

Le chemin `ROLES_TOML` existant permet un manifeste par projet. La première
version n'ajoute pas de fusion implicite de plusieurs fichiers ni de paramètres
arbitraires transmis par le bus. Le lanceur hérite du manifeste sélectionné.

`reasoning` absent signifie « laisser le client utiliser son défaut ». Une valeur
explicite, y compris `standard`, doit être validée et traduite par l'adaptateur.
`standard` n'est pas supposé synonyme de `medium` ou d'absence. Une capacité
inconnue ou non prise en charge produit une erreur explicite avant lancement.
Changer de profil ne conserve pas le raisonnement, le fallback ou les permissions
du profil précédent. Aucun basculement automatique entre providers.

Les noms de provider identifient la configuration, pas une URL construite ni une
preuve de connexion. Les secrets restent dans les mécanismes d'authentification
du client ou son environnement ; jamais dans le manifeste ni le dry-run.

## Résolution et adaptateurs

Une résolution commune produit une structure : rôle, projet, profil, client,
provider, modèle, raisonnement demandé, permissions natives, prompt et skills.
Le code Python existant peut évoluer en module commun testé, appelé par les
scripts shell : une seule résolution pour le lancement et l'installation.

Chaque adaptateur assure :

1. Validation de sa configuration et présence du binaire.
2. Vérification des capacités nécessaires à la version du client utilisée.
3. Chargement des instructions de rôle et des skills, sans supposer que tous
   les clients comprennent les mêmes commandes slash ou chemins de skills.
4. Construction d'un tableau d'arguments et des modifications d'environnement.
5. Exécution et propagation du code de sortie.

Les trois adaptateurs initiaux sont `claude-code`, `codex`, `kimi-cli`. Le contrat
doit permettre un adaptateur local supplémentaire enregistré explicitement par
chemin ; le manifeste est une configuration locale de confiance. Pas de `eval`,
pas de commande shell libre issue d'un message reçu sur le bus.

Les options exactes, versions minimales et mappings de raisonnement seront
vérifiés dans l'aide des binaires et la documentation officielle pendant
l'implémentation. Cette conception ne prétend pas que chaque client accepte
les mêmes niveaux, permissions, providers ou mécanismes de fallback.

Le dry-run montre le profil résolu, la commande expurgée et la méthode de
chargement des instructions, sans démarrer d'agent ni publier sur le bus.
Le contrôle de facturation Anthropic actuel reste propre à l'adaptateur Claude.

## Permissions, skills et observabilité

La mission « revue seulement » reste exprimée dans les instructions ; les limites
effectives sont celles du client. Une option native de permissions ne doit jamais
être traduite silencieusement en mode plus permissif. Le rôle sécurité nécessite
la lecture du dépôt, des vérifications locales maîtrisées et la publication de sa
revue ; toute exécution de code du dépôt suit les permissions de la session.

Les skills bus communs utilisent des identités de rôle et des procédures communes.
Les différences de chargement, reprise de session et interruption relèvent des
adaptateurs ou de leurs annexes. Les exemples spécifiques sont étiquetés.
L'installation ne remplace aucun fichier utilisateur existant ; elle vérifie les
skills requis avant de laisser une installation partielle.

L'identité sur le bus reste indépendante du provider. Cette évolution ne résout
pas le cas existant de plusieurs instances du même rôle sous un identifiant unique.
Un redémarrage avec un autre client conserve la procédure de reprise et ne
réinitialise pas les curseurs de messages implicitement.

Les métadonnées de lancement peuvent indiquer client, provider, profil, modèle et
raisonnement demandé. Ne pas présenter ces valeurs comme une observation du
modèle réellement servi. La consommation absente est « indisponible », jamais
zéro ; ne pas attribuer des mesures Claude à un agent Codex ou Kimi. La collecte
complète pour chaque provider est une extension séparée, pas un prérequis.

## Contrat du rôle security

`foureyes` conserve la revue fonctionnelle, les régressions, les tests et la
maintenabilité. `security` examine les frontières de confiance : entrées non
fiables, commandes shell, chemins, secrets, autorisations, scripts d'installation,
dépendances et instructions reçues d'autres agents ou outils.

Un premier audit peut couvrir le dépôt entier. Ensuite le master peut demander une
revue ciblée sur un diff et ses chemins d'exécution pertinents, en particulier pour
les changements d'exécution, d'accès ou de dépendances. Le rôle est lancé à la
demande (`pop`), sans boucle de surveillance permanente.

Entrée de revue : tâche, périmètre, révision de base et révision examinée. Sortie :

- périmètre et révision réellement examinés ;
- constat avec fichier/ligne, preuve, conditions d'exploitation et impact ;
- gravité et confiance séparées ;
- proposition de correction et vérification attendue ;
- limites de couverture et points non vérifiés.

Chaque constat nouveau est comparé aux demandes et constats déjà suivis. S'il
recoupe une demande `foureyes`, la revue référence celle-ci et ajoute l'impact
sécurité ; elle ne crée pas automatiquement une seconde tâche. Le master garde
la responsabilité d'affecter la correction. Un désaccord entre reviewers est
exposé avec les preuves, jamais résolu en écrasant le verdict de l'autre.

Les résultats sont consultatifs dans cette première version. Pas de veto de
merge automatique, pas de modification du dépôt audité, pas d'exposition de
secrets dans les rapports. Un correctif proposé peut être décrit ou fourni comme
patch à examiner, sans être appliqué. Une revue sans constat n'est pas une
certification. Les mécanismes existants de request/report servent au suivi.

## Compatibilité et livraison

Un manifeste sans version conserve le chemin Claude historique, avec ses valeurs
et son traitement de clé API. Le schéma v2 est explicite ; mélanger les anciens
champs d'exécution dans un rôle v2 est une erreur indiquant comment migrer.
Le retour au manifeste historique reste possible sans changement du protocole bus.

Ordre de livraison et validations : voir le
[plan d'implémentation](../plans/2026-09-27-provider-neutral-security.md).
