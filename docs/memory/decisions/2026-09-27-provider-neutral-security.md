---
id: 2026-09-27-provider-neutral-security
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-09-27
last_verified: 2026-09-27
author: codex
confidence: verified
---

## Finding/decision

La cible confirmée par l'utilisateur est une configuration de rôles indépendante
du provider, avec Claude Code, Codex et Kimi CLI en premiers clients, équipe
homogène ou mixte et raisonnement optionnel par rôle. Un rôle security examine
le dépôt et propose des corrections en complément de foureyes.

L'implémentation conserve le manifeste historique Claude et ajoute le schéma v2,
des profils sélectionnés en bloc, les adaptateurs Claude Code/Codex/Kimi CLI et un
contrat d'adaptateur local extensible. L'utilisateur a précisé pendant la réalisation
que **l'effort reste celui du client/provider par défaut** : aucune surcharge n'est
imposée. Une surcharge explicite nécessite une déclaration des niveaux supportés
par le modèle ; Kimi CLI n'a pas de mapping d'effort vérifié dans cette version.

Le rôle security est pop et consultatif. Son skill propose des corrections sans
les appliquer ; foureyes conserve la revue fonctionnelle. L'installation des skills
vérifie les sources et collisions avant écriture. Le lancement efface l'identifiant
de session Claude hérité pour éviter d'attribuer le transcript du parent à un enfant.

## When it applies

Évolution des lanceurs, du manifeste de rôles et des skills de coordination/revue.

## Evidence

- Besoins explicités dans la conversation utilisateur du 2026-09-27, retranscrits
  dans la [conception](../../superpowers/specs/2026-09-27-provider-neutral-security-design.md).
- [Guide livré](../../AGENT-PROFILES.md), [résolveur/adaptateurs](../../../scripts/lib/role_config.py)
  et [tests de lancement isolés](../../../tests/test_role_config.py).
- [Plan de réalisation](../../superpowers/plans/2026-09-27-provider-neutral-security.md).
- État des scripts examiné à la révision `00fe199`, sources détaillées dans la conception.

## Limits / what would invalidate it

Les aides locales de Claude Code 2.1.283, Codex 0.157.1 et Kimi CLI 0.41.0 ont été
examinées. Les tests utilisent des clients simulés ; aucun appel authentifié aux
modèles ni essai sur le bus actif n'a été effectué. Le support d'un niveau d'effort
par le modèle reste une déclaration de configuration, pas une découverte distante.
Les métriques demeurent fondées sur les sources Claude et les snapshots conservés
restent historiques. Le guide décrit ces limites et les références officielles.
Cette note ne donne aucune permission de déployer ou de changer un bus actif.
