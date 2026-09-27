# Plan : profils d'agents et revue sécurité

Statut : implémenté et testé localement ; appels authentifiés et activation opérationnelle non effectués. Référence :
[conception](../specs/2026-09-27-provider-neutral-security-design.md).

## 1. Résolveur de profils et compatibilité

Faire évoluer `scripts/lib/roles.sh` et introduire un résolveur Python commun.
Conserver le manifeste historique ; ajouter le schéma v2, les profils et leur
validation. Rejeter profils inconnus, champs incompatibles et mélange v1/v2.

Validation : fixtures TOML pour équipe homogène, mixte, défaut, surcharge par rôle,
profil inconnu, niveaux explicites et absence de niveau. Vérifier qu'un changement
de client ne transporte pas les options d'un autre profil. Exécuter les tests de
rôles existants sans modifier leur sens pour faire passer la compatibilité.

## 2. Adaptateurs et lancement

Relever les versions et aides disponibles de Claude Code, Codex et Kimi CLI ;
consulter les documentations officielles pour les capacités et options utilisées.
Documenter cette matrice avec date et limites. Implémenter le contrat d'adaptateur
et déplacer la construction Claude hors du lanceur commun. Vérifier également la
propagation de `ROLES_TOML` par bootstrap, spawn et les templates de workspace.

Validation avec faux exécutables : arguments exacts, prompt et skills chargés,
environnement, secrets absents du dry-run, chemins avec espaces, propagation des
échecs, binaire absent, option non supportée, comportement historique de clé API.
Les tests ne lancent pas de sessions facturées et ne contactent pas le bus actif.
Un essai réel par client reste distinct des tests de construction de commande.

## 3. Portabilité des instructions et métadonnées

Adapter `scripts/link-role-skills.sh`, les prompts sous `roles/` et les skills bus.
Séparer procédures communes et particularités du client. Vérifier le chargement
effectif des skills, pas seulement leur présence dans un répertoire.
Auditer la collecte et l'affichage des métriques avant toute extension : sources
Claude seulement pour les sessions Claude, valeurs inconnues affichées comme telles.

Validation : installation isolée et idempotente, refus d'écrasement de fichiers
utilisateur, échec anticipé si skill absent, équipe mixte sans fausse métrique.
Conserver les procédures de reprise du board et des curseurs.

## 4. Rôle et skill security

Créer le prompt `roles/security.md` et le skill `skills/agent-bus-security/SKILL.md`
selon le contrat de conception. Ajouter le rôle pop au manifeste, adapter les
tests qui énumèrent exactement les rôles et préciser le partage avec foureyes.
Utiliser le skill de création de skills lors de cette étape.

Validation sur exemples : problème fonctionnel seul, injection avérée, hypothèse
non prouvée, doublon déjà suivi par foureyes, secret à expurger, revue sans constat.
Vérifier que les propositions n'appliquent aucun patch et ne créent pas de veto.

## 5. Documentation et recette

Documenter trois clients distincts, Kimi CLI explicitement, et la distinction
client/provider. Fournir des exemples homogènes et mixtes avec modèles vérifiés
au moment de la livraison, raisonnement optionnel, migration et ajout d'adaptateur.
Exécuter les suites de tests ciblées et les contrôles mémoire. Consigner séparément
tests simulés et essais réels ; ne pas déclarer un client validé sur la seule base
du dry-run. Toute activation opérationnelle doit rester explicite.

## Résultat et précisions de livraison

Le [guide utilisateur](../../AGENT-PROFILES.md) décrit le schéma effectivement livré,
les options natives, les sources vérifiées et le contrat JSON des adaptateurs locaux.
Les profils fournis n'imposent aucun effort, conformément à la précision utilisateur.
Les tests de lancement utilisent des exécutables simulés ; les aides/version locales
des trois clients ont été consultées. La suite shell, les 13 tests de profils, les
13 tests mémoire, le contrôle des liens et la validation du skill ont été exécutés.
La collecte de métriques n'a pas été étendue : seule l'identité de session héritée
est supprimée au lancement, et les instructions signalent les données indisponibles.
Les exemples de modèles non configurés restent explicitement à personnaliser :
aucun compte/modèle disponible n'est déduit d'une simple aide CLI.
