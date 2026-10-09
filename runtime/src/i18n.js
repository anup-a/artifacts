// The comment panel speaks the READER's language, read from the browser's
// navigator.languages. Never the published page's <html lang>: a Chinese
// reader may open an English page and still wants Chinese buttons.
//
// One row per message: [English, zh-CN, zh-TW, ja, ko, es, pt-BR, fr, de].
// English is the key. A "[context] " prefix tells apart two uses of the same
// English word that other languages word differently; English drops it.
// "%s" is replaced by t()'s arguments in order. Keep
// translations as short as the English: the panel is 380px wide.
// test/i18n.test.mjs checks every row has all nine and the same %s count.

export const LANGS = ["en", "zh-CN", "zh-TW", "ja", "ko", "es", "pt-BR", "fr", "de"];

export const ROWS = [
  ["Comments", "评论", "留言", "コメント", "댓글", "Comentarios", "Comentários", "Commentaires", "Kommentare"],
  ["Comment", "评论", "留言", "コメント", "댓글 달기", "Comentar", "Comentar", "Commenter", "Kommentieren"],
  ["[send] Comment", "评论", "留言", "コメント", "등록", "Comentar", "Comentar", "Commenter", "Senden"],
  ["Open comments", "打开评论", "開啟留言", "コメントを開く", "댓글 열기", "Abrir comentarios", "Abrir comentários", "Ouvrir les commentaires", "Kommentare öffnen"],
  ["Close", "关闭", "關閉", "閉じる", "닫기", "Cerrar", "Fechar", "Fermer", "Schließen"],
  ["Close comments", "关闭评论", "關閉留言", "コメントを閉じる", "댓글 닫기", "Cerrar comentarios", "Fechar comentários", "Fermer les commentaires", "Kommentare schließen"],
  ["Version", "版本", "版本", "バージョン", "버전", "Versión", "Versão", "Version", "Version"],
  ["Filter comments", "筛选评论", "篩選留言", "コメントを絞り込む", "댓글 필터", "Filtrar comentarios", "Filtrar comentários", "Filtrer les commentaires", "Kommentare filtern"],
  ["Open", "未解决", "未解決", "未解決", "미해결", "Abiertos", "Abertos", "Ouverts", "Offen"],
  ["Resolved", "已解决", "已解決", "解決済み", "해결됨", "Resueltos", "Resolvidos", "Résolus", "Erledigt"],
  ["All", "全部", "全部", "すべて", "전체", "Todos", "Todos", "Tous", "Alle"],
  ["Select text or", "选中文字或", "選取文字或", "テキストを選択するか", "텍스트를 선택하거나", "Selecciona texto o", "Selecione um texto ou", "Sélectionnez du texte ou", "Text markieren oder"],
  ["Point at an element", "选择页面元素", "選取頁面元素", "要素を指定", "요소 지정", "Señala un elemento", "Aponte um elemento", "Désignez un élément", "Element auswählen"],
  ["Click an element to comment", "点击要评论的元素", "點選要留言的元素", "コメントする要素をクリック", "댓글을 달 요소를 클릭하세요", "Haz clic en un elemento", "Clique em um elemento", "Cliquez sur un élément", "Element anklicken"],
  ["Anonymous", "匿名", "匿名", "匿名", "익명", "Anónimo", "Anônimo", "Anonyme", "Anonym"],
  ["Author", "作者", "作者", "作成者", "작성자", "Autor", "Autor", "Auteur", "Autor"],
  ["Verified email", "已验证邮箱", "已驗證電子郵件", "認証済みのメール", "인증된 이메일", "Correo verificado", "E-mail verificado", "E-mail vérifié", "Verifizierte E-Mail"],
  ["✓ verified", "✓ 已验证", "✓ 已驗證", "✓ 認証済み", "✓ 인증됨", "✓ verificado", "✓ verificado", "✓ vérifié", "✓ verifiziert"],
  ["Moved", "已偏移", "已偏移", "位置変更", "위치 변경", "Movido", "Movido", "Déplacé", "Verschoben"],
  [
    "The quoted text changed, so this comment now points at the closest match.",
    "引用的文字已更改，此评论现在指向最接近的位置。",
    "引用的文字已變更，此留言現在指向最接近的位置。",
    "引用したテキストが変わったため、このコメントは最も近い箇所を指しています。",
    "인용한 텍스트가 바뀌어 이 댓글은 가장 가까운 부분을 가리킵니다.",
    "El texto citado cambió; este comentario apunta ahora a la coincidencia más cercana.",
    "O texto citado mudou; este comentário agora aponta para o trecho mais próximo.",
    "Le texte cité a changé : ce commentaire pointe désormais vers le passage le plus proche.",
    "Der zitierte Text hat sich geändert; der Kommentar zeigt jetzt auf die ähnlichste Stelle.",
  ],
  ["Earlier version", "较早版本", "較早版本", "以前のバージョン", "이전 버전", "Versión anterior", "Versão anterior", "Version antérieure", "Frühere Version"],
  [
    "The quoted text is not on this version of the page.",
    "此版本的页面中没有引用的文字。",
    "此版本的頁面中沒有引用的文字。",
    "引用したテキストはこのバージョンのページにありません。",
    "인용한 텍스트가 이 버전의 페이지에 없습니다.",
    "El texto citado no está en esta versión de la página.",
    "O texto citado não está nesta versão da página.",
    "Le texte cité ne figure pas dans cette version de la page.",
    "Der zitierte Text steht nicht in dieser Version der Seite.",
  ],
  ["The quoted text is not on this version.", "此版本中没有引用的文字。", "此版本中沒有引用的文字。", "引用したテキストはこのバージョンにありません。", "인용한 텍스트가 이 버전에 없습니다.", "El texto citado no está en esta versión.", "O texto citado não está nesta versão.", "Le texte cité ne figure pas dans cette version.", "Der zitierte Text steht nicht in dieser Version."],
  ["Seen", "已读", "已讀", "既読", "확인함", "Visto", "Visto", "Vu", "Gesehen"],
  ["The author has read this comment.", "作者已读过这条评论。", "作者已讀過這則留言。", "作成者がこのコメントを読みました。", "작성자가 이 댓글을 읽었습니다.", "El autor ha leído este comentario.", "O autor leu este comentário.", "L'auteur a lu ce commentaire.", "Der Autor hat diesen Kommentar gelesen."],
  ["Working on it", "处理中", "處理中", "対応中", "작업 중", "En curso", "Em andamento", "En cours", "In Arbeit"],
  ["The author is changing the page for this comment.", "作者正在根据这条评论修改页面。", "作者正在根據這則留言修改頁面。", "作成者がこのコメントに沿ってページを修正中です。", "작성자가 이 댓글에 따라 페이지를 수정하고 있습니다.", "El autor está cambiando la página por este comentario.", "O autor está alterando a página por causa deste comentário.", "L'auteur modifie la page suite à ce commentaire.", "Der Autor ändert die Seite wegen dieses Kommentars."],
  ["Declined", "未采纳", "未採納", "見送り", "반려됨", "Rechazado", "Recusado", "Refusé", "Abgelehnt"],
  [
    "The author decided not to change this. Their reason is in the replies.",
    "作者决定不做修改，原因见回复。",
    "作者決定不做修改，原因請見回覆。",
    "作成者は変更しないことにしました。理由は返信にあります。",
    "작성자가 변경하지 않기로 했습니다. 이유는 답글에 있습니다.",
    "El autor decidió no cambiarlo. El motivo está en las respuestas.",
    "O autor decidiu não alterar. O motivo está nas respostas.",
    "L'auteur a choisi de ne pas modifier ce passage. La raison est dans les réponses.",
    "Der Autor ändert das nicht. Die Begründung steht in den Antworten.",
  ],
  ["%s reply", "%s 条回复", "%s 則回覆", "返信 %s 件", "답글 %s개", "%s respuesta", "%s resposta", "%s réponse", "%s Antwort"],
  ["%s replies", "%s 条回复", "%s 則回覆", "返信 %s 件", "답글 %s개", "%s respuestas", "%s respostas", "%s réponses", "%s Antworten"],
  ["Reply as %s…", "以 %s 的身份回复…", "以 %s 的身分回覆…", "%s として返信…", "%s(으)로 답글 달기…", "Responder como %s…", "Responder como %s…", "Répondre en tant que %s…", "Als %s antworten…"],
  ["Reply", "回复", "回覆", "返信", "답글", "Respuesta", "Resposta", "Réponse", "Antwort"],
  ["Send reply", "发送回复", "送出回覆", "返信を送信", "답글 보내기", "Enviar respuesta", "Enviar resposta", "Envoyer la réponse", "Antwort senden"],
  ["Could not post reply: %s", "回复发送失败：%s", "回覆送出失敗：%s", "返信を投稿できませんでした：%s", "답글을 게시하지 못했습니다: %s", "No se pudo publicar la respuesta: %s", "Não foi possível publicar a resposta: %s", "Impossible de publier la réponse : %s", "Antwort konnte nicht gesendet werden: %s"],
  ["No comments yet", "还没有评论", "還沒有留言", "コメントはまだありません", "아직 댓글이 없습니다", "Aún no hay comentarios", "Nenhum comentário ainda", "Aucun commentaire pour l'instant", "Noch keine Kommentare"],
  ["No open comments", "没有未解决的评论", "沒有未解決的留言", "未解決のコメントはありません", "미해결 댓글이 없습니다", "No hay comentarios abiertos", "Nenhum comentário aberto", "Aucun commentaire ouvert", "Keine offenen Kommentare"],
  ["No resolved comments", "没有已解决的评论", "沒有已解決的留言", "解決済みのコメントはありません", "해결된 댓글이 없습니다", "No hay comentarios resueltos", "Nenhum comentário resolvido", "Aucun commentaire résolu", "Keine erledigten Kommentare"],
  ["Select any text on the page to start a thread.", "选中页面上的任意文字，即可发起讨论。", "選取頁面上的任何文字，即可開始討論。", "ページのテキストを選択してスレッドを始めましょう。", "페이지의 텍스트를 선택해 스레드를 시작하세요.", "Selecciona cualquier texto de la página para abrir un hilo.", "Selecione qualquer texto da página para abrir uma conversa.", "Sélectionnez du texte sur la page pour ouvrir une discussion.", "Markiere Text auf der Seite, um einen Thread zu starten."],
  ["Anything new you comment on will show up here.", "你新发表的评论会显示在这里。", "你新增的留言會顯示在這裡。", "新しいコメントはここに表示されます。", "새로 단 댓글이 여기에 표시됩니다.", "Lo que comentes aparecerá aquí.", "Seus novos comentários aparecem aqui.", "Vos nouveaux commentaires apparaîtront ici.", "Neue Kommentare erscheinen hier."],
  ["On an earlier version", "在较早的版本上", "在較早的版本上", "以前のバージョン", "이전 버전", "En una versión anterior", "Em uma versão anterior", "Sur une version antérieure", "In einer früheren Version"],
  ["Comments are unavailable right now.", "评论暂时不可用。", "留言暫時無法使用。", "現在コメントを利用できません。", "지금은 댓글을 사용할 수 없습니다.", "Los comentarios no están disponibles ahora.", "Os comentários estão indisponíveis no momento.", "Les commentaires sont indisponibles pour le moment.", "Kommentare sind gerade nicht verfügbar."],
  ["Comments unavailable.", "评论不可用。", "留言無法使用。", "コメントを利用できません。", "댓글을 사용할 수 없습니다.", "Comentarios no disponibles.", "Comentários indisponíveis.", "Commentaires indisponibles.", "Kommentare nicht verfügbar."],
  ["Viewing v%s. The current version is v%s.", "正在查看 v%s，当前版本是 v%s。", "正在檢視 v%s，目前版本是 v%s。", "v%s を表示しています。現在のバージョンは v%s です。", "v%s 버전을 보고 있습니다. 현재 버전은 v%s입니다.", "Estás viendo la v%s. La versión actual es la v%s.", "Você está vendo a v%s. A versão atual é a v%s.", "Vous consultez la v%s. La version actuelle est la v%s.", "Du siehst v%s. Die aktuelle Version ist v%s."],
  ["Add comment", "添加评论", "新增留言", "コメントを追加", "댓글 추가", "Comentar", "Comentar", "Commenter", "Kommentieren"],
  ["New comment", "新评论", "新留言", "新しいコメント", "새 댓글", "Nuevo comentario", "Novo comentário", "Nouveau commentaire", "Neuer Kommentar"],
  ["Your name", "你的名字", "你的名字", "お名前", "이름", "Tu nombre", "Seu nome", "Votre nom", "Dein Name"],
  ["Your comment", "评论内容", "留言內容", "コメント", "댓글 내용", "Tu comentario", "Seu comentário", "Votre commentaire", "Dein Kommentar"],
  ["Commenting as %s", "以 %s 的身份评论", "以 %s 的身分留言", "%s としてコメント", "%s(으)로 댓글 작성", "Comentas como %s", "Comentando como %s", "Vous commentez en tant que %s", "Du kommentierst als %s"],
  ["change", "更改", "變更", "変更", "변경", "cambiar", "alterar", "modifier", "ändern"],
  ["Add a comment…", "写下评论…", "寫下留言…", "コメントを入力…", "댓글을 입력하세요…", "Escribe un comentario…", "Escreva um comentário…", "Écrivez un commentaire…", "Kommentar schreiben…"],
  ["Suggest an edit", "建议修改", "建議修改", "修正を提案", "수정 제안", "Sugerir un cambio", "Sugerir alteração", "Proposer une modification", "Änderung vorschlagen"],
  ["Suggested text", "建议的文字", "建議的文字", "提案するテキスト", "제안하는 텍스트", "Texto sugerido", "Texto sugerido", "Texte proposé", "Vorgeschlagener Text"],
  ["Why? (optional)", "原因（可选）", "原因（選填）", "理由（任意）", "이유 (선택)", "¿Por qué? (opcional)", "Por quê? (opcional)", "Pourquoi ? (facultatif)", "Warum? (optional)"],
  ["Just comment", "仅评论", "僅留言", "コメントのみ", "댓글만 달기", "Solo comentar", "Só comentar", "Commenter seulement", "Nur kommentieren"],
  ["Suggested edit", "建议修改", "建議修改", "修正の提案", "수정 제안", "Cambio sugerido", "Alteração sugerida", "Modification proposée", "Änderungsvorschlag"],
  ["Cancel", "取消", "取消", "キャンセル", "취소", "Cancelar", "Cancelar", "Annuler", "Abbrechen"],
  ["Write something first.", "请先写点内容。", "請先寫點內容。", "先に内容を入力してください。", "먼저 내용을 입력하세요.", "Escribe algo primero.", "Escreva algo primeiro.", "Écrivez d'abord quelque chose.", "Schreib zuerst etwas."],
  ["Posting…", "发布中…", "發布中…", "投稿中…", "게시 중…", "Publicando…", "Publicando…", "Publication…", "Wird gesendet…"],
  ["Could not post.", "发布失败。", "發布失敗。", "投稿できませんでした。", "게시하지 못했습니다.", "No se pudo publicar.", "Não foi possível publicar.", "Publication impossible.", "Senden fehlgeschlagen."],
  ["Press %s to post", "按 %s 发布", "按 %s 發布", "%s で投稿", "%s 키로 게시", "Pulsa %s para publicar", "Pressione %s para publicar", "Appuyez sur %s pour publier", "%s zum Senden drücken"],
  ["timed out", "请求超时", "請求逾時", "タイムアウトしました", "시간 초과", "tiempo de espera agotado", "tempo esgotado", "délai dépassé", "Zeitüberschreitung"],
  ["network error", "网络错误", "網路錯誤", "ネットワークエラー", "네트워크 오류", "error de red", "erro de rede", "erreur réseau", "Netzwerkfehler"],
];

/**
 * The supported language for a list of BCP 47 tags, in preference order:
 * zh, zh-CN, zh-Hans and zh-SG are Simplified; zh-TW, zh-HK, zh-MO and
 * zh-Hant are Traditional; any pt is pt-BR; the rest match on the base
 * language. The first tag we speak wins, so "en" before "zh" stays English.
 */
export function pickLang(tags) {
  for (const raw of tags || []) {
    const parts = String(raw).toLowerCase().replace(/_/g, "-").split("-");
    const base = parts[0];
    if (base === "zh") {
      if (parts.includes("hans")) return "zh-CN";
      return parts.some((p) => p === "hant" || p === "tw" || p === "hk" || p === "mo") ? "zh-TW" : "zh-CN";
    }
    if (base === "pt") return "pt-BR";
    if (LANGS.includes(base)) return base;
  }
  return "en";
}

function browserTags() {
  try {
    const nav = globalThis.navigator;
    if (!nav) return [];
    if (nav.languages && nav.languages.length) return nav.languages;
    return nav.language ? [nav.language] : [];
  } catch {
    return [];
  }
}

export const LANG = pickLang(browserTags());

const tables = new Map();
function table(lang) {
  let m = tables.get(lang);
  if (!m) {
    const col = LANGS.indexOf(lang);
    m = new Map(col > 0 ? ROWS.map((r) => [r[0], r[col]]) : []);
    tables.set(lang, m);
  }
  return m;
}

/** Fill each %s in turn with the next argument. */
export function format(s, args) {
  let i = 0;
  return s.replace(/%s/g, () => (i < args.length ? String(args[i++]) : "%s"));
}

/** Translate msg into lang (default: the reader's language); English if missing. */
export function tl(lang, msg, ...args) {
  return format(table(lang).get(msg) || msg.replace(/^\[[^\]]*\] /, ""), args);
}

/** Translate msg into the reader's language. */
export function t(msg, ...args) {
  return tl(LANG, msg, ...args);
}

// Chinese, Japanese and Korean have one form; French and Portuguese count
// zero as singular. Mirrors internal/i18n's oneForm.
function oneForm(lang, n) {
  if (lang === "fr" || lang === "pt-BR") return n === 0 || n === 1;
  return n === 1;
}

/** tn(3, "%s reply", "%s replies") is "3 replies"; the count fills the first %s. */
export function tn(n, one, other, ...args) {
  return t(oneForm(LANG, n) ? one : other, n, ...args);
}
