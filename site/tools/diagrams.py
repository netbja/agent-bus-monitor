"""Hand-drawn inline SVG diagrams for the site (one idea per figure).

Every colour is a CSS class (assets/style.css, `.dia ...`), so light and dark come from variables.
Roles: you (amber), bus (green), agent (blue), svc (violet), err (red), plain (ink).
Text is passed through `t(en, fr)` by the caller's language.
"""
from html import escape as esc

ROLES = ["ink", "you", "bus", "agent", "svc", "err"]


class Fig:
    def __init__(self, fid, w, h, title, desc):
        self.id, self.w, self.h, self.title, self.desc = fid, w, h, title, desc
        self.parts = []

    # ----- primitives
    def add(self, s):
        self.parts.append(s)

    def box(self, x, y, w, h, role, title, subs=(), mono_subs=False, rx=10):
        self.add(f'<rect class="box r-{role}" x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}"/>')
        n = len(subs)
        top = y + h / 2 - (n * 7.5) + 4.5 if title else y + h / 2 - (n * 7.5) + 12
        if title:
            self.add(f'<text class="ttl" x="{x + w / 2}" y="{top}" text-anchor="middle">{esc(title)}</text>')
        cls = "sub mono" if mono_subs else "sub"
        for i, s in enumerate(subs):
            yy = top + 18 + i * 15 if title else top + i * 15
            self.add(f'<text class="{cls}" x="{x + w / 2}" y="{yy}" text-anchor="middle">{esc(s)}</text>')

    def group(self, x, y, w, h, label=None):
        self.add(f'<rect class="group" x="{x}" y="{y}" width="{w}" height="{h}" rx="14"/>')
        if label:
            self.add(f'<text class="sub mono" x="{x + 14}" y="{y + 19}">{esc(label)}</text>')

    def line(self, d, role="ink", dash=False, end=True, start=False):
        cls = "ln" + (f" l-{role}" if role != "ink" else "") + (" dash" if dash else "")
        ms = f' marker-start="url(#{self.id}-a-{role})"' if start else ""
        me = f' marker-end="url(#{self.id}-a-{role})"' if end else ""
        self.add(f'<path class="{cls}" d="{d}"{ms}{me}/>')

    def label(self, x, y, text, mono=False, anchor="middle"):
        cw = 6.7 if mono else 6.0
        w = len(text) * cw + 12
        x0 = x - w / 2 if anchor == "middle" else x - 6
        self.add(f'<rect class="mask" x="{x0:.1f}" y="{y - 12}" width="{w:.1f}" height="17" rx="4"/>')
        self.add(f'<text class="lbl{" mono" if mono else ""}" x="{x}" y="{y}" text-anchor="{anchor}">{esc(text)}</text>')

    def text(self, x, y, s, cls="sub", anchor="middle"):
        self.add(f'<text class="{cls}" x="{x}" y="{y}" text-anchor="{anchor}">{esc(s)}</text>')

    def diamond(self, cx, cy, w, h, lines, role="plain"):
        pts = f"{cx},{cy - h / 2} {cx + w / 2},{cy} {cx},{cy + h / 2} {cx - w / 2},{cy}"
        self.add(f'<polygon class="box r-{role}" points="{pts}"/>')
        y0 = cy - (len(lines) - 1) * 8 + 4
        for i, s in enumerate(lines):
            self.add(f'<text class="lbl" x="{cx}" y="{y0 + i * 16}" text-anchor="middle">{esc(s)}</text>')

    def render(self):
        defs = "".join(
            f'<marker id="{self.id}-a-{r}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="9" markerHeight="9" '
            f'markerUnits="userSpaceOnUse" orient="auto-start-reverse"><path class="ah{"" if r == "ink" else "-" + r}" d="M0 0L10 5L0 10z"/></marker>'
            for r in ROLES)
        return (f'<svg viewBox="0 0 {self.w} {self.h}" role="img" aria-labelledby="{self.id}-t {self.id}-d">'
                f'<title id="{self.id}-t">{esc(self.title)}</title><desc id="{self.id}-d">{esc(self.desc)}</desc>'
                f'<defs>{defs}</defs>{"".join(self.parts)}</svg>')


def legend(items):
    """items: (role, text) or ('dash', text) or ('line', text)."""
    out = []
    for kind, text in items:
        if kind in ("you", "bus", "agent", "svc", "err"):
            out.append(f'<li><i class="sw sw-{kind}"></i>{esc(text)}</li>')
        elif kind == "dash":
            out.append(f'<li><i class="ls dashed"></i>{esc(text)}</li>')
        else:
            out.append(f'<li><i class="ls"></i>{esc(text)}</li>')
    return '<ul class="d-legend">' + "".join(out) + "</ul>"


# ------------------------------------------------------------------ figure 1: who does what
def fig_architecture(t):
    f = Fig("f1", 900, 500, t("Who does what around the bus", "Qui fait quoi autour du bus"),
            t("The person in busmon, the agents in herdr tabs and two project services all read and write one Redis Streams bus.",
              "La personne dans busmon, les agents dans des onglets herdr et deux services du projet lisent et écrivent sur un même bus Redis Streams."))
    f.group(40, 330, 840, 150, t("herdr workspace · one tab per agent", "espace herdr · un onglet par agent"))
    for i, a in enumerate(["master", "coder", "foureyes", "sentinel"]):
        f.box(60 + i * 205, 366, 180, 90, "agent", a, [t("a client in its pane", "un client dans son panneau")])
    f.box(30, 30, 220, 100, "you", t("You · busmon", "Vous · busmon"),
          [t("terminal dashboard", "tableau de bord en terminal"), t("answers, directives, reviews", "réponses, directives, revues")])
    f.box(300, 110, 280, 170, "bus", t("Redis Streams bus", "Bus Redis Streams"),
          ["project:status · project:report", "project:notify · project:cmd", "project:verdicts · project:board…",
           t("one namespace per project", "un espace de noms par projet")], mono_subs=False)
    f.group(645, 10, 240, 290, t("project services", "services du projet"))
    f.box(660, 38, 210, 94, "svc", t("Conductor", "Conducteur"), ["agentbus missions", t("merges, tests, opens reviews", "fusionne, teste, ouvre les revues")])
    f.box(660, 166, 210, 106, "svc", t("Dispatcher", "Répartiteur"), ["agentbus dispatch", t("wakes idle agents", "réveille les agents inactifs")])
    f.line("M140 130 V195 H300", "you", start=True)
    f.label(220, 199, t("reads · writes", "lit · écrit"))
    f.line("M660 85 H620 V150 H580", "svc", start=True)
    f.label(620, 118, t("fenced", "protégé"), True)
    f.line("M660 219 H580", "svc", start=True)
    f.label(620, 223, t("fenced", "protégé"), True)
    f.line("M440 280 V330", "agent", start=True)
    f.label(440, 309, "status · subscribe --drain", True)
    f.line("M765 272 V330", "svc", dash=True)
    f.label(765, 306, t("types one wake line", "tape une ligne de réveil"))
    return f.render() + legend([("you", t("you", "vous")), ("bus", t("bus", "bus")), ("agent", t("agents", "agents")),
                                ("svc", t("services", "services")), ("line", t("reads and writes", "lit et écrit")),
                                ("dash", t("types into a pane", "tape dans un panneau"))])


# ------------------------------------------------------------------ figure 2: life of a message
def fig_message(t):
    f = Fig("f2", 900, 560, t("The life of a message and the wake line", "La vie d'un message et la ligne de réveil"),
            t("A sender writes a directive to Redis; the dispatcher checks the agent's pane and types one fixed wake line; the agent drains its messages from Redis.",
              "Un émetteur écrit une directive dans Redis ; le répartiteur vérifie le panneau de l'agent et tape une ligne de réveil fixe ; l'agent vide ses messages depuis Redis."))
    xs = {"s": 100, "r": 310, "d": 545, "a": 790}
    names = {"s": (t("Sender", "Émetteur"), t("master, or you", "master, ou vous"), "you"),
             "r": ("Redis", "project:cmd", "bus"),
             "d": (t("Dispatcher", "Répartiteur"), t("a service", "un service"), "svc"),
             "a": (t("Agent", "Agent"), t("in its herdr pane", "dans son panneau herdr"), "agent")}
    for k, x in xs.items():
        n, s, r = names[k]
        f.add(f'<line class="life" x1="{x}" y1="70" x2="{x}" y2="450"/>')
        f.box(x - 80, 14, 160, 54, r, n, [s])
    f.line("M100 112 H310", "you")
    f.label(205, 106, t('cmd coder "…"  → XADD', 'cmd coder "…"  → XADD'), True)
    f.line("M545 160 H310", "svc")
    f.label(430, 154, t("what is owed? agent not armed", "que doit-on ? agent non armé"))
    f.add('<rect class="box r-svc" x="440" y="188" width="210" height="46" rx="8"/>')
    f.text(545, 207, t("pane ready, unfocused,", "panneau prêt, sans focus,"), "lbl")
    f.text(545, 223, t("process identity verified", "identité du processus vérifiée"), "lbl")
    f.line("M545 284 H790", "svc", dash=True)
    f.label(668, 278, t("types the wake line", "tape la ligne de réveil"))
    f.line("M790 330 H310", "agent")
    f.label(550, 324, "subscribe --drain", True)
    f.line("M310 372 H790", "bus")
    f.label(550, 366, t("entries: unacked first, then unread", "entrées : non acquittées d'abord, puis non lues"))
    f.line("M790 420 H310", "agent")
    f.label(550, 414, t("ack after output · report · status", "acquittement après sortie · rapport · état"))
    f.add('<rect class="box r-you" x="20" y="470" width="860" height="70" rx="10"/>')
    f.text(36, 492, t("The only text the dispatcher ever types:", "Le seul texte que le répartiteur tape jamais :"), "ttl", "start")
    f.text(36, 513, "[agentbus#<nonce>] <project>: 1 message(s) waiting for coder.", "sub mono", "start")
    f.text(36, 529, "Run: agentbus --project <project> subscribe --drain coder", "sub mono", "start")
    return f.render() + legend([("line", t("write / read", "écriture / lecture")), ("dash", t("typed into the pane", "tapé dans le panneau"))])


# ------------------------------------------------------------------ figure 3: dispatcher decision flow
def fig_dispatch(t):
    f = Fig("f3", 900, 730, t("The dispatcher's decision for one agent", "La décision du répartiteur pour un agent"),
            t("A flowchart of the rules, in order: nothing owed, armed, already escalated or out of tries, inside the retry window, pane not verifiable, pane not ready, otherwise type the wake line.",
              "Un organigramme des règles, dans l'ordre : rien à livrer, agent armé, déjà escaladé ou sans essais, dans le délai de reprise, panneau invérifiable, panneau pas prêt, sinon taper la ligne de réveil."))
    cx = 190
    f.box(90, 8, 200, 34, "svc", t("for each agent, every pass", "pour chaque agent, à chaque passe"), [], rx=17)
    ys = [108, 204, 300, 396, 492, 588]
    qs = [
        ([t("Anything waiting", "Quelque chose attend"), t("for this agent?", "cet agent ?")], "no", t("nothing to do · rule 1", "rien à faire · règle 1"), "plain"),
        ([t("Is the agent armed", "L'agent est-il armé"), t("(its own subscribe runs)?", "(son subscribe tourne) ?")], "yes", t("leave it alone · rule 2", "ne rien faire · règle 2"), "plain"),
        ([t("Escalated already,", "Déjà escaladé,"), t("or out of tries?", "ou sans essais ?")], "yes", t("wait for progress, or escalate once · 3–4", "attendre du progrès, ou escalader une fois · 3–4"), "err"),
        ([t("Prompted within the", "Invité dans le"), t("retry window?", "délai de reprise ?")], "yes", t("wait · rule 5", "attendre · règle 5"), "plain"),
        ([t("Pane found, identity", "Panneau trouvé, identité"), t("verified, client known?", "vérifiée, client connu ?")], "no", t("defer, escalate past the age limit · 6–7", "différer, escalader passé le délai · 6–7"), "you"),
        ([t("Idle, ready, not busy,", "Inactif, prêt, pas occupé,"), t("not focused?", "sans focus ?")], "no", t("defer, escalate past the age limit · 8", "différer, escalader passé le délai · 8"), "you"),
    ]
    prev = 42
    for y, (lines, stop, outcome, orole) in zip(ys, qs):
        f.diamond(cx, y, 330, 60, lines)
        f.line(f"M{cx} {prev} V{y - 30}", "svc")
        f.line(f"M{cx + 165} {y} H500", "svc" if orole == "plain" else orole)
        f.label(428, y + 4, stop, True)
        f.box(500, y - 23, 380, 46, orole, outcome, [], rx=8)
        prev = y + 30
    # the "continue" labels sit on the spine between diamonds
    for i, y in enumerate(ys[:-1]):
        cont = "yes" if qs[i][1] == "no" else "no"
        f.label(cx, y + 53, cont, True)
    f.line(f"M{cx} {ys[-1] + 30} V654", "bus")
    f.label(cx, ys[-1] + 53, "yes", True)
    f.box(60, 654, 260, 56, "bus", t("PROMPT · rule 9", "INVITER · règle 9"), [t("type the wake line, once", "taper la ligne de réveil, une fois")], rx=10)
    f.add(f'<text class="sub" x="500" y="686" text-anchor="start">{esc(t("A message or an identity change resets the tries.", "Un nouveau message ou un changement d'identité remet les essais à zéro."))}</text>')
    return f.render() + legend([("line", t("flow", "flux")), ("err", t("escalation", "escalade")), ("you", t("waiting for the pane", "attente du panneau")),
                                ("bus", t("the one action", "la seule action"))])


# ------------------------------------------------------------------ figure 4: 4-eyes
def fig_foureyes(t):
    f = Fig("f4", 900, 520, t("The 4-eyes review and the verdict ledger", "La revue à quatre yeux et le registre des verdicts"),
            t("The author challenges a reviewer, the reviewer records a verdict in an append-only ledger, and a roll-up decides: approved only if the latest independent approve is newer than any reject.",
              "L'auteur lance un défi à un relecteur, le relecteur consigne un verdict dans un registre en ajout seul, et un cumul tranche : approuvé seulement si la dernière approbation indépendante est plus récente que tout rejet."))
    f.box(20, 36, 190, 76, "agent", t("Author · coder", "Auteur · coder"), ["agentbus challenge"])
    f.box(335, 36, 200, 76, "bus", t("Gate on the reviewer", "Barrière sur le relecteur"), [t("a lock, not the record", "un verrou, pas la trace")])
    f.box(660, 36, 220, 76, "agent", t("Reviewer · foureyes", "Relecteur · foureyes"), [t("reads the change, answers", "lit le changement, répond")])
    f.line("M210 74 H335", "agent")
    f.label(272, 68, "challenge", True)
    f.line("M535 74 H660", "bus")
    f.label(597, 68, t("wakes", "réveille"))
    f.box(660, 200, 220, 80, "bus", t("Verdict ledger", "Registre des verdicts"), [t("append-only · every verdict kept", "ajout seul · chaque verdict gardé"), t("a self-verdict is never counted", "un auto-verdict n'est jamais compté")])
    f.line("M770 112 V200", "agent")
    f.label(770, 160, "verdict approve|reject", True)
    f.line("M660 240 H435 V112", "bus", dash=True)
    f.label(548, 234, t("closes the gate", "ferme la barrière"))
    f.box(300, 330, 330, 84, "bus", t("Roll-up", "Cumul"),
          [t("APPROVED iff the latest independent", "APPROUVÉ ssi la dernière approbation"), t("approve is newer than any reject", "indépendante est plus récente que tout rejet")])
    f.line("M770 280 V372 H630", "bus")
    f.label(770, 330, "agentbus verdicts", True)
    outs = [(345, "ok", "APPROVED", "exit 0", "bus"), (465, "w", "PENDING", "exit 2", "you"), (585, "r", "REJECTED", "exit 3", "err")]
    for x, _, name, code, role in outs:
        f.line(f"M{x} 414 V444", role)
        f.box(x - 55, 444, 110, 34, role, name, [], rx=8)
        f.text(x, 496, code, "sub mono")
    f.box(20, 424, 190, 66, "bus", t("Delivery", "Livraison"), [t("master or docs, with the", "master ou docs, avec le"), t("exact commit approved", "commit exact approuvé")])
    f.line("M290 461 H210", "bus")
    return f.render() + legend([("agent", t("agents", "agents")), ("bus", t("bus state", "état du bus")), ("you", t("not decided yet", "pas encore tranché")),
                                ("err", t("rejected", "rejeté")), ("dash", t("side effect", "effet de bord"))])


# ------------------------------------------------------------------ figure 5: layers
def fig_layers(t):
    f = Fig("f5", 900, 470, t("The layers that make a restart safe", "Les couches qui rendent un redémarrage sûr"),
            t("Five layers, from Redis persistence at the bottom to once-per-episode marks at the top. Each one removes a failure of the layer below.",
              "Cinq couches, de la persistance Redis en bas aux marques « une fois par épisode » en haut. Chacune supprime une défaillance de la couche du dessous."))
    layers = [
        ("svc", "5", t("Once per episode", "Une fois par épisode"), t("A notice and its mark are written in one script,", "Un avis et sa marque s'écrivent dans un même script,"), t("so a restarted service never repeats it.", "un service redémarré ne le répète donc jamais."), "FencedCmdOnce"),
        ("svc", "4", t("Fenced writes", "Écritures protégées"), t("Every write first checks the service's lease token.", "Chaque écriture vérifie d'abord le jeton de bail du service."), t("A stale service, or a deleted project, writes nothing.", "Un service périmé, ou un projet supprimé, n'écrit rien."), t("Lua · lease token", "Lua · jeton de bail")),
        ("svc", "3", t("Stateless services", "Services sans état"), t("Dispatcher and conductor keep no durable local state:", "Répartiteur et conducteur ne gardent aucun état local durable :"), t("each decision is recomputed from Redis and herdr.", "chaque décision est recalculée depuis Redis et herdr."), "systemd --user · launchd"),
        ("bus", "2", t("Per-agent queues", "Files par agent"), t("One consumer group per agent: at-least-once delivery,", "Un groupe de consommateurs par agent : livraison au moins une fois,"), t("unacknowledged entries come back first.", "les entrées non acquittées reviennent d'abord."), t("24 h horizon", "horizon de 24 h")),
        ("bus", "1", t("Redis persistence", "Persistance de Redis"), t("The broker writes an append-only file", "Le courtier écrit un fichier en ajout seul"), t("to a named Docker volume.", "dans un volume Docker nommé."), "--appendonly yes"),
    ]
    y = 14
    for role, n, title, l1, l2, tag in layers:
        f.add(f'<rect class="box r-{role}" x="60" y="{y}" width="820" height="78" rx="12"/>')
        f.add(f'<circle class="numdot" cx="92" cy="{y + 39}" r="15"/><text class="num" x="92" y="{y + 43}" text-anchor="middle">{n}</text>')
        f.add(f'<text class="ttl" x="124" y="{y + 36}">{esc(title)}</text>')
        f.add(f'<text class="sub" x="330" y="{y + 33}">{esc(l1)}</text><text class="sub" x="330" y="{y + 52}">{esc(l2)}</text>')
        f.add(f'<text class="sub mono" x="868" y="{y + 20}" text-anchor="end">{esc(tag)}</text>')
        y += 90
    f.line("M30 450 V20", "ink")
    f.add(f'<text class="sub mono" transform="translate(20 235) rotate(-90)" text-anchor="middle">{esc(t("builds on", "s'appuie sur"))}</text>')
    return f.render() + legend([("bus", t("data", "données")), ("svc", t("services", "services"))])


# ------------------------------------------------------------------ figure 6: suspend / resume
def fig_suspend(t):
    f = Fig("f6", 900, 470, t("Suspend and resume", "Suspendre et reprendre"),
            t("A state diagram: running, waiting for quiet, suspended, resuming. A timeout or a signal cancels a suspend; a missing agent keeps a resume suspended.",
              "Un diagramme d'états : en marche, attente du calme, suspendu, reprise. Un délai dépassé ou un signal annule une suspension ; un agent manquant garde la reprise suspendue."))
    f.box(30, 175, 160, 76, "bus", t("Running", "En marche"), [t("team and services up", "équipe et services actifs")])
    f.box(260, 56, 190, 84, "svc", t("Waiting for quiet", "Attente du calme"), [t("project paused; two quiet", "projet en pause ; deux tours"), t("rounds, up to suspend.wait", "calmes, jusqu'à suspend.wait")])
    f.box(520, 175, 190, 84, "you", t("Suspended", "Suspendu"), [t("services stopped,", "services arrêtés,"), t("snapshot kept", "instantané gardé")])
    f.box(260, 300, 190, 84, "svc", t("Resuming", "Reprise"), [t("services start, agents", "services lancés, agents"), t("relaunched and proven", "relancés et vérifiés")])
    f.line("M110 175 V98 H260", "svc")
    f.label(185, 92, "agentbus suspend", True)
    f.line("M450 98 H615 V175", "svc")
    f.label(540, 92, t("quiet twice", "calme deux fois"))
    f.line("M615 259 V342 H450", "svc")
    f.label(540, 336, "agentbus resume", True)
    f.line("M260 342 H110 V251", "bus")
    f.label(185, 336, t("agents alive", "agents vivants"))
    f.line("M330 140 V213 H190", "err", dash=True)
    f.label(330, 180, t("timeout · signal", "délai · signal"))
    f.line("M400 300 V217 H520", "err", dash=True)
    f.label(400, 262, t("agent missing", "agent manquant"))
    f.add(f'<text class="sub" x="30" y="418">{esc(t("Cancelled: the services that were active start again, then the suspend is cancelled.", "Annulé : les services qui étaient actifs redémarrent, puis la suspension est annulée."))}</text>')
    f.add(f'<text class="sub" x="30" y="438">{esc(t("A missing agent keeps the suspend, unless --partial. Under any pause the dispatcher types nothing.", "Un agent manquant garde la suspension, sauf --partial. Sous toute pause, le répartiteur ne tape rien."))}</text>')
    f.add(f'<text class="sub" x="30" y="458">{esc(t("After a reboot, suspend.auto_resume (off by default) lets the herdr workspace hook start the resume.", "Après un redémarrage, suspend.auto_resume (désactivé par défaut) laisse le crochet de l'espace herdr lancer la reprise."))}</text>')
    return f.render() + legend([("bus", t("running", "en marche")), ("svc", t("transition", "transition")), ("you", t("rest state", "état de repos")),
                                ("dash", t("not completed", "non abouti"))])
