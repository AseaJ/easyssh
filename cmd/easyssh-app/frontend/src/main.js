// easyssh 仪表盘前端。
// 通过 window.go.app.App 调用 Go 端绑定方法(Wails 运行时注入)。
const go = () => window.go.app.App;

const toastEl = document.getElementById("toast");
let currentView = "overview";
let configState = null; // 当前编辑中的配置(避免每次渲染都从后端取,保存时统一收集)

function toast(msg, ok = true) {
  toastEl.textContent = msg;
  toastEl.className = "toast " + (ok ? "ok" : "error");
  toastEl.classList.remove("hidden");
  clearTimeout(toastEl._t);
  toastEl._t = setTimeout(() => toastEl.classList.add("hidden"), 3200);
}

// 全局错误捕获:任何渲染/调用异常都显示出来,便于定位
window.addEventListener("error", (e) => {
  toast("脚本错误: " + (e.message || e.error), false);
  console.error("[easyssh] 脚本错误:", e.error || e.message);
});
window.addEventListener("unhandledrejection", (e) => {
  toast("调用失败: " + (e.reason || e), false);
  console.error("[easyssh] 未处理拒绝:", e.reason);
});

const statusMeta = {
  fresh: { label: "正常", cls: "green" },
  renewing: { label: "待续期", cls: "yellow" },
  retry: { label: "重试中", cls: "red" },
  expired: { label: "已过期", cls: "red" },
  未签发: { label: "未签发", cls: "gray" },
};

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

// ---------- 视图切换 ----------
function switchView(v) {
  currentView = v;
  document.querySelectorAll(".tab").forEach((t) =>
    t.classList.toggle("active", t.dataset.view === v)
  );
  ["overview", "config", "notify", "logs"].forEach((name) => {
    const el = document.getElementById("view-" + name);
    const hidden = name !== v;
    if (el._prevHidden === undefined) el._prevHidden = !hidden;
    const wasHidden = el._prevHidden;
    el._prevHidden = hidden;
    el.classList.toggle("hidden", hidden);
    // 从隐藏切到显示时重放进入动画
    if (wasHidden && !hidden) {
      el.classList.remove("view");
      void el.offsetWidth;
      el.classList.add("view");
    }
  });
  if (v === "config") loadConfigForm();
  if (v === "notify") loadNotifyForm();
  if (v === "logs") refreshLogs(true);
}

document.querySelectorAll(".tab").forEach((tab) => {
  tab.addEventListener("click", () => switchView(tab.dataset.view));
});

// ---------- 概览 ----------
async function refresh() {
  try {
    const [overview, certs] = await Promise.all([
      go().GetOverview(),
      go().ListCertificates(),
    ]);
    renderOverview(overview);
    renderCerts(certs);
  } catch (e) {
    toast(String(e), false);
  }
}

function renderOverview(ov) {
  const cards = [
    { label: "托管证书", num: ov.total, cls: "blue" },
    { label: "正常", num: ov.healthy, cls: "green" },
    { label: "待续期", num: ov.expiring, cls: "yellow" },
    { label: "异常", num: ov.failed, cls: "red" },
  ];
  document.getElementById("overview").innerHTML = cards
    .map(
      (c) => `
      <div class="card">
        <div class="label">${c.label}</div>
        <div class="num ${c.cls}">${c.num}</div>
      </div>`
    )
    .join("");
  // 缺失密钥提示(先清旧再插,避免累积)
  document.querySelectorAll(".missing-env").forEach((el) => el.remove());
  if (ov.missing_env && ov.missing_env.length > 0) {
    const warn = document.createElement("div");
    warn.className = "missing-env";
    warn.innerHTML = `⚠️ 以下环境变量未设置(密钥缺失),请在「配置」页的密钥栏填入明文: <b>${esc(ov.missing_env.join(", "))}</b>`;
    document.getElementById("overview").after(warn);
  }
  // 注意后端 JSON 字段为 last_run(snake_case)
  document.getElementById("last-run").textContent =
    `调度: ${ov.schedule} · 上次操作: ${ov.last_run}`;
}

function renderCerts(certs) {
  const wrap = document.getElementById("cert-list");
  wrap.classList.add("cert-list");
  if (certs.length === 0) {
    wrap.innerHTML = `<div class="empty-hint">配置中暂无证书条目</div>`;
    return;
  }
  const rows = certs
    .map((c) => {
      const m = statusMeta[c.status] || statusMeta["未签发"];
      const remain = c.status === "未签发" ? "—" : `${c.remain_days}d`;
      const remainCls =
        c.status === "expired" ? "overdue" : c.status === "renewing" ? "warn" : "";
      return `
      <div class="cert-row">
        <div class="cert-main" title="${esc(c.name)}">
          <span class="dot ${m.cls}"></span>
          <span class="cert-name">${esc(c.name)}</span>
          <span class="badge ${m.cls}">${m.label}</span>
        </div>
        <div class="cert-domains" title="${esc(c.domains.join(", "))}">${esc(c.domains.join(", "))}</div>
        <div class="cert-remain ${remainCls}">${remain}</div>
        <div class="cert-expiry">${esc(c.not_after || "—")}</div>
        <div class="cert-actions">
          <button data-act="renew" data-name="${esc(c.name)}" title="立即续期:重新签发新证书并自动部署到所有目标(续期+部署组合操作)">立即续期</button>
          <button data-act="deploy" data-name="${esc(c.name)}">手动部署</button>
        </div>
      </div>`;
    })
    .join("");
  wrap.innerHTML = rows;
}

// 立即续期/部署:按钮禁用显示进度,并自动跳转到日志界面观察实时日志
document.getElementById("cert-list").addEventListener("click", async (e) => {
  const btn = e.target.closest("button[data-act]");
  if (!btn || btn.disabled) return;
  const { act, name } = btn.dataset;
  const orig = btn.textContent;
  btn.disabled = true;
  btn.textContent = act === "renew" ? "续期中…" : "部署中…";  switchView("logs");
  refreshLogs(true);
  try {
    const msg = act === "renew" ? await go().Renew(name) : await go().Deploy(name);
    toast(msg);
    await refresh();
  } catch (err) {
    toast(String(err), false);
  } finally {
    btn.disabled = false;
    btn.textContent = orig;
  }
});

// ---------- 表单辅助 ----------
// 字段悬停提示:鼠标悬停在 label 上显示解释文字
const FIELD_HINTS = {
  // 基础设置
  ca_server: "ACME 证书颁发机构的目录地址。默认是 Let's Encrypt 测试环境(staging),跑通后换成正式环境 https://acme-v02.api.letsencrypt.org/directory",
  ca_email: "用于 ACME 账号注册的邮箱,证书到期前 CA 会通过该邮箱联系你。同一个账号邮箱对应同一对账号密钥",
  account_key: "ACME 账号密钥文件路径。首次运行时自动生成,请妥善保管,换密钥会影响已签发证书的续期",
  check_interval: "守护进程扫描一次配置的时间间隔,格式如 6h、30m。到点自动检查证书是否临近续期",
  renew_before: "证书剩余有效期低于该值时触发自动续期,格式如 30d、7d",
  retry_backoff: "续期/部署失败后的重试间隔序列(逗号分隔)。第一次失败等 1h,再失败等 6h,依次类推,用完后固定用最后一个",
  // SSH 主机
  host_name: "该主机的唯一标识名,供证书条目里的 SSH 部署目标引用。改名后,引用它的部署目标会同步更新",
  host_host: "远程服务器 IP 或域名,如 203.0.113.10 或 srv.example.com",
  host_port: "SSH 端口,默认 22",
  host_user: "登录远程服务器的用户名",
  host_key: "SSH 私钥文件路径(如 ~/.ssh/id_ed25519)。留空则尝试使用 ssh-agent",
  host_remote_path: "证书部署到远程服务器的目录,如 /etc/nginx/ssl/example",
  host_reload_cmd: "证书更新后远程执行的命令,如 nginx -t && nginx -s reload。留空则默认执行 nginx -t && nginx -s reload",
  // 证书配置
  name: "证书条目名称,用于区分多个证书,如 example",
  domains: "需要签发的域名列表(逗号分隔)。含通配符(如 *.example.com)时必须使用 dns-01 挑战",
  challenge: "域名所有权验证方式。http-01 需要 80 端口可访问;dns-01 通过 DNS 解析验证,支持通配符,需要配置 DNS 服务商密钥",
  dns_provider: "dns-01 挑战使用的 DNS 服务商。选好后在下方配置对应的 API 密钥",
  storage_dir: "证书产物保存目录(含 cert.pem/fullchain.pem/privkey.pem 等),请勿与其他条目共用",
  // SSH 部署目标
  host_ref: "选择复用哪个 SSH 主机的连接参数。选中后下方内联字段可留空;也可以不选,直接在内联字段里填写完整连接信息",
  reload_cmd: "证书更新后远程执行的命令(可选)。留空时使用 SSH 主机里配置的 reload 命令",
  cert_filename: "远程证书文件名(可选,默认 fullchain.pem)。一般无需修改,仅当远程服务指定了特殊文件名时使用",
  key_filename: "远程私钥文件名(可选,默认 privkey.pem)。一般无需修改,仅当远程服务指定了特殊文件名时使用",
  remote_path: "证书部署到远程服务器的目录。已引用 SSH 主机时留空即可复用主机的远程目录",
  // 通知
  smtp_host: "SMTP 服务器地址,如 smtp.qq.com。留空则不启用邮件通知",
  smtp_port: "SMTP 端口。SSL 加密通常 465,STARTTLS 通常 587",
  smtp_user: "SMTP 登录账号(通常是邮箱地址本身)",
  smtp_pass: "SMTP 授权码/密码。明文填写会自动转存为系统环境变量,配置文件中只保存引用",
  smtp_to: "通知收件人,每行一个,支持多个",
  webhook: "推送通知的 URL(可选),收到事件时以 JSON 形式推送",
  notify_expiring: "证书进入续期窗口(剩余时间低于续期阈值)时推送提醒,24 小时内最多一次",
  notify_success: "签发/续期/部署成功后推送,附新到期时间",
  // DNS 密钥
  dns_key: "密钥对应的环境变量名,由 DNS 服务商定义。如 DNSPOD_API_KEY、CF_DNS_API_TOKEN、ALICLOUD_ACCESS_KEY",
  dns_val: "密钥值。明文填写会自动转存为系统环境变量,配置文件只保留 {{env:VAR}} 引用,不暴露明文",
  dns_opt_polling: "DNSPOD 轮询 DNS 记录生效的间隔(秒),默认 60。一般无需修改",
  dns_opt_timeout: "DNSPOD 等待 DNS 记录生效的超时时间(秒),默认 120。生效慢时适当调大",
};

// DNS 非敏感参数:以固定输入框明文展示,直接写入配置(不转环境变量)
const DNS_OPT_META = [
  { key: "DNSPOD_POLLING_INTERVAL", name: "dns_opt_polling", label: "轮询间隔(秒)", def: "60" },
  { key: "DNSPOD_PROPAGATION_TIMEOUT", name: "dns_opt_timeout", label: "传播超时(秒)", def: "120" },
];
const isNonSecretDnsKey = (k) =>
  DNS_OPT_META.some((m) => m.key.toUpperCase() === String(k || "").trim().toUpperCase());

function fieldHint(name) {
  const hint = FIELD_HINTS[name];
  return hint ? `<span class="hint" data-hint="${esc(hint)}">?</span>` : "";
}

function field(label, name, value, opts = {}) {
  const v = esc(value ?? "");
  const cls = opts.cls ? ` ${opts.cls}` : "";
  const styleAttr = opts.style ? ` style="display:${opts.style}"` : "";
  if (opts.type === "textarea") {
    return `<div class="field${cls}"${styleAttr}><label>${esc(label)}${fieldHint(name)}</label>
      <textarea name="${name}" rows="${opts.rows || 2}">${v}</textarea></div>`;
  }
  if (opts.options) {
    const optsHtml = opts.options
      .map((o) => `<option value="${o}" ${o === value ? "selected" : ""}>${o}</option>`)
      .join("");
    return `<div class="field${cls}"${styleAttr}><label>${esc(label)}${fieldHint(name)}</label>
      <select name="${name}">${optsHtml}</select></div>`;
  }
  return `<div class="field${cls}"${styleAttr}><label>${esc(label)}${fieldHint(name)}</label>
    <input name="${name}" value="${v}" placeholder="${esc(opts.placeholder || "")}" ${opts.type === "password" ? 'type="password"' : ""}/></div>`;
}

// 开关行(toggle switch)
function switchField(label, name, checked, desc) {
  return `
    <label class="switch-row">
      <span class="switch-text">
        <b>${esc(label)}</b>
        ${desc ? `<span class="muted">${esc(desc)}</span>` : ""}
      </span>
      <input type="checkbox" name="${name}" ${checked ? "checked" : ""} />
      <span class="switch"></span>
    </label>`;
}

function qv(name) {
  return document.querySelector(`[name="${name}"]`)?.value ?? "";
}

// ---------- 字段悬停提示(tooltip) ----------
// 统一挂到 body 的事件委托,任何动态渲染的字段提示都可生效
document.addEventListener("mouseover", (e) => {
  const hint = e.target.closest(".hint");
  if (!hint || hint.dataset.hint === undefined) return;
  const r = hint.getBoundingClientRect();
  let tip = document.getElementById("field-tooltip");
  if (!tip) {
    tip = document.createElement("div");
    tip.id = "field-tooltip";
    document.body.appendChild(tip);
  }
  tip.textContent = hint.dataset.hint;
  tip.classList.remove("hidden");
  // 优先显示在提示图标上方,空间不足则下方
  const tw = tip.offsetWidth;
  let left = r.left + r.width / 2 - tw / 2;
  left = Math.max(8, Math.min(left, window.innerWidth - tw - 8));
  const above = r.top - tip.offsetHeight - 8;
  tip.style.left = left + "px";
  if (above > 8) {
    tip.style.top = above + "px";
  } else {
    tip.style.top = (r.bottom + 8) + "px";
  }
});
document.addEventListener("mouseout", (e) => {
  if (e.target.closest(".hint")) {
    document.getElementById("field-tooltip")?.classList.add("hidden");
  }
});

// qvOr:表单存在该字段用表单值(允许清空),否则回退到后端当前值。
// 用于配置页保存时避免覆盖通知页(独立视图)的字段。
function qvOr(name, fallback) {
  const el = document.querySelector(`[name="${name}"]`);
  return el ? el.value : fallback;
}

function checkedOr(name, fallback) {
  const el = document.querySelector(`[name="${name}"]`);
  return el ? el.checked : fallback;
}

// ---------- 配置页:分类导航 ----------
function switchCfgCat(cat) {
  document.querySelectorAll(".cfg-cat").forEach((c) =>
    c.classList.toggle("active", c.dataset.cat === cat)
  );
  ["basic", "hosts", "certs"].forEach((name) => {
    document.getElementById("cfg-" + name).classList.toggle("hidden", name !== cat);
  });
  const titles = { basic: "基础设置", hosts: "SSH 主机", certs: "证书配置" };
  document.getElementById("cfg-title").textContent = titles[cat] || cat;
}

document.getElementById("view-config").addEventListener("click", (e) => {
  const cat = e.target.closest(".cfg-cat");
  if (cat) switchCfgCat(cat.dataset.cat);
});

// ---------- 配置表单 ----------
function deployFieldsHtml(d, idx, certName) {
  let extra = "";
  switch (d.type) {
    case "nginx":
      extra = field("reload 命令", "reload_cmd", d.reload_cmd, { placeholder: "nginx -s reload" }) +
        field("校验命令", "test_cmd", d.test_cmd, { placeholder: "nginx -t" }) +
        field("证书路径(可选)", "cert_path", d.cert_path) +
        field("私钥路径(可选)", "key_path", d.key_path);
      break;
    case "ssh": {
      const hostOpts = (configState?.hosts || []).map((h) => h.name);
      const usingRef = !!d.host_ref;
      // 内联字段带 ssh-inline 类,引用主机时隐藏(字段级控制,保持 grid 布局一致)
      const inline = `
        ${field("主机", "host", d.host, { cls: "ssh-inline", style: usingRef ? "none" : "" })}
        ${field("端口", "port", d.port ?? 22, { cls: "ssh-inline", style: usingRef ? "none" : "" })}
        ${field("用户", "user", d.user, { cls: "ssh-inline", style: usingRef ? "none" : "" })}
        ${field("私钥路径", "ssh_key", d.ssh_key, { cls: "ssh-inline", style: usingRef ? "none" : "", placeholder: "~/.ssh/id_ed25519" })}
        ${field("远程目录", "remote_path", d.remote_path, { cls: "ssh-inline", style: usingRef ? "none" : "" })}
        ${field("reload 命令", "reload_cmd", d.reload_cmd, { cls: "ssh-inline", style: usingRef ? "none" : "", placeholder: "nginx -t && nginx -s reload" })}
        ${field("证书文件名(可选,默认 fullchain.pem)", "cert_filename", d.cert_filename)}
        ${field("私钥文件名(可选,默认 privkey.pem)", "key_filename", d.key_filename)}`;
      extra = field("引用主机(SSH 主机定义,优先)", "host_ref", d.host_ref, { options: ["", ...hostOpts] }) + inline;
      break;
    }
    case "file":
      extra = field("输出目录", "dir", d.dir);
      break;
    case "webhook":
      extra = field("URL", "url", d.url);
      break;
  }
  const testBtn = d.type === "ssh"
    ? sshTestBtn(sshDeployKey(certName, d))
    : "";
  return `
    <div class="deploy-card" data-deploy="${idx}">
      <div class="deploy-head">
        <b>部署目标 ${idx + 1}</b>
        <div class="actions">${testBtn}<button class="btn danger small" data-rm-deploy="${idx}">删除</button></div>
      </div>
      <div class="grid">
        ${field("类型", "type", d.type, { options: ["nginx", "ssh", "file", "webhook"] })}
        ${extra}
      </div>
    </div>`;
}

function certCardHtml(c, ci) {
  const dnsKeys = c.dns_opts || {};
  // 非敏感参数(轮询间隔/传播超时)分离出来:以固定明文输入框展示(后端已展开为明文)
  const optVals = {};
  DNS_OPT_META.forEach((m) => {
    const hit = Object.keys(dnsKeys).find((k) => k.toUpperCase() === m.key);
    optVals[m.name] = hit ? dnsKeys[hit] : "";
  });
  const secretRows = Object.entries(dnsKeys).filter(([k]) => !isNonSecretDnsKey(k));
  const dnsOptsHtml = secretRows
    .map(([k, v]) => `
      <div class="grid dns-row">
        ${field("密钥名(环境变量名)", "dns_key", k, { placeholder: "DNSPOD_API_KEY" })}
        ${field("密钥值(明文自动保存为环境变量)", "dns_val", v, { placeholder: "ID,Token" })}
        <div class="field"><label>&nbsp;</label><button class="btn danger small" data-rm-dns="${ci}">删除</button></div>
      </div>`)
    .join("");
  // dnspod 时展示可直改的明文参数;其他服务商若配置了同名参数也展示
  const isDnspod = c.dns_provider === "dnspod";
  const optFields = DNS_OPT_META.map((m) => {
    const show = isDnspod || optVals[m.name] !== "";
    if (!show) return "";
    const val = optVals[m.name] !== "" ? optVals[m.name] : m.def;
    return field(m.label, m.name, val, { placeholder: m.def });
  }).filter(Boolean);
  // 轮询间隔/传播超时合并到同一排(grid 内两个字段并排)
  const optHtml = optFields.length
    ? `<div class="grid dns-row dns-opt-row">${optFields.join("")}</div>`
    : "";
  const deploysHtml = (c.deploys || [])
    .map((d, di) => deployFieldsHtml(d, di, c.name))
    .join("");
  const certName = esc(c.name) || `证书条目 ${ci + 1}`;
  // 已有密钥时隐藏「添加密钥」按钮(密钥一个就够),无密钥时展示
  const addDnsBtn = secretRows.length === 0
    ? `<button class="btn ghost small section-add-btn" data-add-dns="${ci}">添加密钥</button>`
    : "";
  // 折叠状态持久化(按证书名)
  const collapsed = certIsCollapsed(c.name) ? " collapsed" : "";

  return `
    <div class="cert-card${collapsed}" data-cert="${ci}">
      <div class="cert-card-head">
        <div class="actions">
          <button class="cert-card-toggle" data-toggle-cert="${ci}" title="折叠/展开">▼</button>
          <b>${certName}</b>
        </div>
        <button class="btn danger small" data-rm-cert="${ci}">删除条目</button>
      </div>
      <div class="cert-card-body">
        <div class="grid">
          ${field("名称", "name", c.name, { placeholder: "example" })}
          ${field("域名(逗号分隔)", "domains", (c.domains || []).join(", "))}
          ${field("挑战方式", "challenge", c.challenge, { options: ["http-01", "dns-01"] })}
          ${field("dns 服务商", "dns_provider", c.dns_provider, { options: ["", "dnspod", "cloudflare", "alidns"] })}
          ${field("存储目录", "storage_dir", c.storage_dir)}
        </div>
        <div class="form-section">
          <h3>DNS 密钥</h3>
          ${isDnspod ? '<div class="muted dns-opt-tip">以下参数为明文配置,可直接修改,保存后写入配置文件(不转环境变量)</div>' : ""}
          ${optHtml}
          ${dnsOptsHtml ? `<div class="dns-keys">${dnsOptsHtml}</div>` : '<div class="muted">未配置密钥</div>'}
          ${addDnsBtn}
        </div>
        <div class="form-section">
          <h3>部署目标</h3>
          ${deploysHtml ? `<div class="deploy-cards">${deploysHtml}</div>` : '<div class="muted">未配置部署</div>'}
          <button class="btn ghost small section-add-btn" data-add-deploy="${ci}">添加部署</button>
        </div>
      </div>
    </div>`;
}

async function loadConfigForm() {
  try {
    const cfg = await go().GetConfig();
    configState = cfg;
    renderConfigForm(cfg);
  } catch (e) {
    toast("加载配置失败: " + (e && e.message ? e.message : e), false);
    console.error("[easyssh] GetConfig 失败:", e);
  }
}

function renderConfigForm(cfg) {
  try {
    const certsHtml = (cfg.certificates || []).map((c, i) => certCardHtml(c, i)).join("");
    const hostsHtml = (cfg.hosts || []).map((h, i) => `
      <div class="host-card" data-host="${i}">
        <div class="grid">
          ${field("主机名", "host_name", h.name, { placeholder: "prod-server" })}
          ${field("主机", "host_host", h.host)}
          ${field("端口", "host_port", h.port ?? 22)}
          ${field("用户", "host_user", h.user)}
          ${field("私钥路径", "host_key", h.key, { placeholder: "~/.ssh/id_ed25519" })}
          ${field("远程目录", "host_remote_path", h.remote_path)}
          ${field("reload 命令", "host_reload_cmd", h.reload_cmd, { placeholder: "nginx -t && nginx -s reload" })}
        </div>
        <div class="host-actions">
          ${sshTestBtn(`host:${h.name}`)}
          <button class="btn danger small" data-rm-host="${i}">删除该主机</button>
        </div>
      </div>`).join("");
    document.getElementById("cfg-basic").innerHTML = `
      <div class="form-section">
        <h3>CA(证书颁发机构)</h3>
        <div class="grid">
          ${field("CA server", "ca_server", cfg.ca_server, { placeholder: "https://acme-v02.api.letsencrypt.org/directory" })}
          ${field("邮箱", "ca_email", cfg.ca_email)}
          ${field("账号密钥路径", "account_key", cfg.account_key)}
        </div>
      </div>
      <div class="form-section">
        <h3>调度</h3>
        <div class="grid">
          ${field("扫描周期", "check_interval", cfg.check_interval, { placeholder: "6h" })}
          ${field("续期阈值", "renew_before", cfg.renew_before, { placeholder: "30d" })}
          ${field("退避序列(逗号分隔)", "retry_backoff", (cfg.retry_backoff || []).join(","))}
        </div>
      </div>
      <div class="form-section">
        <h3>系统</h3>
        <div class="switch-group">
          ${switchField("开机自启", "autostart", !!cfg.autostart, "登录 Windows 后自动以托盘后台模式运行,证书续期/部署不再依赖手动打开应用")}
        </div>
        <div class="muted" style="margin-top:4px;font-size:12px">启用后,关闭窗口将隐藏到系统托盘继续后台运行;点托盘「退出」才真正退出。</div>
      </div>`;
    document.getElementById("cfg-hosts").innerHTML = `
      ${hostsHtml || '<div class="muted" style="padding:8px 0 12px">未定义主机,可在此新增或删除</div>'}
      <button id="btn-add-host" class="btn ghost small">新增 SSH 主机</button>`;
    document.getElementById("cfg-certs").innerHTML =
      `<div class="cfg-cat-actions">
        <button id="btn-add-cert" class="btn ghost">新增证书条目</button>
      </div>` +
      (certsHtml || '<div class="muted" style="padding:8px 0">未配置证书条目</div>');
  } catch (e) {
    toast("渲染配置表单失败: " + (e && e.message ? e.message : e), false);
    console.error("[easyssh] 渲染表单失败:", e);
  }
}

// 收集表单 → ConfigView
// 通知相关字段(smtp/webhook/开关)在独立「通知」页维护,此处回退到后端当前值,
// 避免在配置页保存时误清空通知设置。
function collectConfig(baseCfg) {
  const base = baseCfg || configState || {};
  const cfg = {
    ca_server: qv("ca_server"),
    ca_email: qv("ca_email"),
    account_key: qv("account_key"),
    check_interval: qv("check_interval"),
    renew_before: qv("renew_before"),
    retry_backoff: (qv("retry_backoff") || "").split(",").map((s) => s.trim()).filter(Boolean),
    webhook: qvOr("webhook", base.webhook ?? ""),
    smtp_host: qvOr("smtp_host", base.smtp_host ?? ""),
    smtp_port: parseInt(qvOr("smtp_port", String(base.smtp_port ?? 465)), 10),
    smtp_user: qvOr("smtp_user", base.smtp_user ?? ""),
    smtp_pass: qvOr("smtp_pass", base.smtp_pass ?? ""),
    smtp_to: qvOr("smtp_to", (base.smtp_to || []).join("\n"))
      .split(/\r?\n/).map((s) => s.trim()).filter(Boolean),
    notify_expiring: checkedOr("notify_expiring", base.notify_expiring ?? false),
    notify_success: checkedOr("notify_success", base.notify_success ?? false),
    autostart: checkedOr("autostart", base.autostart ?? false),
    hosts: [],
    certificates: [],
  };
  document.querySelectorAll(".host-card").forEach((hc, hi) => {
    const hg = (n) => hc.querySelector(`[name="${n}"]`)?.value ?? "";
    // cert_filename/key_filename 已从表单移除(迁移到 SSH 部署目标),
    // 若旧配置 host 级已有值则保留,避免保存时误清空
    const oldHost = base.hosts?.[hi];
    cfg.hosts.push({
      name: hg("host_name"),
      host: hg("host_host"),
      port: parseInt(hg("host_port") || "22", 10),
      user: hg("host_user"),
      key: hg("host_key"),
      remote_path: hg("host_remote_path"),
      reload_cmd: hg("host_reload_cmd"),
      cert_filename: oldHost?.cert_filename ?? "",
      key_filename: oldHost?.key_filename ?? "",
    });
  });
  document.querySelectorAll(".cert-card").forEach((card) => {
    const get = (n) => card.querySelector(`[name="${n}"]`)?.value ?? "";
    const cert = {
      name: get("name"),
      domains: (get("domains") || "").split(",").map((s) => s.trim()).filter(Boolean),
      challenge: get("challenge"),
      dns_provider: get("dns_provider"),
      storage_dir: get("storage_dir"),
      dns_opts: {},
      deploys: [],
    };
    // DNS 密钥(可能多行,data-dns 未用索引区分,按顺序成对)
    // 先收集非敏感明文参数,再收集自定义密钥键值对
    const dnsKeys = card.querySelectorAll('[name="dns_key"]');
    const dnsVals = card.querySelectorAll('[name="dns_val"]');
    DNS_OPT_META.forEach((m) => {
      const v = (card.querySelector(`[name="${m.name}"]`)?.value ?? "").trim();
      if (v) cert.dns_opts[m.key] = v;
    });
    dnsKeys.forEach((k, i) => {
      const key = k.value.trim();
      if (key && !isNonSecretDnsKey(key)) cert.dns_opts[key] = dnsVals[i]?.value ?? "";
    });
    card.querySelectorAll(".deploy-card").forEach((dc) => {
      const dg = (n) => dc.querySelector(`[name="${n}"]`)?.value ?? "";
      cert.deploys.push({
        type: dg("type"),
        reload_cmd: dg("reload_cmd"),
        test_cmd: dg("test_cmd"),
        cert_path: dg("cert_path"),
        key_path: dg("key_path"),
        host: dg("host"),
        host_ref: dg("host_ref"),
        port: parseInt(dg("port") || "0", 10),
        user: dg("user"),
        ssh_key: dg("ssh_key"),
        remote_path: dg("remote_path"),
        cert_filename: dg("cert_filename"),
        key_filename: dg("key_filename"),
        dir: dg("dir"),
        url: dg("url"),
      });
      // 修正:host_ref 已选时,丢弃 ssh 内联字段(避免隐藏字段旧值残留)
      const d = cert.deploys[cert.deploys.length - 1];
      if (d.type === "ssh" && d.host_ref) {
        d.host = "";
        d.port = 0;
        d.user = "";
        d.ssh_key = "";
        d.remote_path = "";
        d.reload_cmd = "";
      }
    });
    cfg.certificates.push(cert);
  });
  return cfg;
}

document.getElementById("btn-save-config").addEventListener("click", async () => {
  const btn = document.getElementById("btn-save-config");
  btn.disabled = true;
  try {
    // 以最新已保存配置为基准,避免覆盖通知页等其他视图的修改
    const latest = await go().GetConfig();
    const cfg = collectConfig(latest);
    // 主机改名同步:若 SSH 主机改名,把证书部署目标里引用的旧名同步为新名
    syncHostRefRename(latest, cfg);
    const msg = await go().SaveConfig(cfg);
    toast(msg);
    await loadConfigForm();
    await refresh();
  } catch (err) {
    toast("保存失败: " + err, false);
  } finally {
    btn.disabled = false;
  }
});

// 主机改名同步:按位置匹配(表单与已保存配置的主机顺序一致),
// 旧名 → 新名建立映射,再更新所有证书部署目标中的 host_ref。
function syncHostRefRename(latest, cfg) {
  const rename = {};
  (latest.hosts || []).forEach((h, i) => {
    const oldName = h.name;
    const newName = cfg.hosts[i]?.name;
    if (oldName && newName && oldName !== newName) rename[oldName] = newName;
  });
  if (Object.keys(rename).length === 0) return;
  (cfg.certificates || []).forEach((c) => {
    (c.deploys || []).forEach((d) => {
      if (d.type === "ssh" && d.host_ref && rename[d.host_ref]) {
        d.host_ref = rename[d.host_ref];
      }
    });
  });
}

// 新增证书条目(切到证书分类并追加卡片)
// 按钮在证书配置内容区动态渲染,使用事件委托处理
function addCertCard() {
  switchCfgCat("certs");
  const wrap = document.getElementById("cfg-certs");
  const idx = document.querySelectorAll(".cert-card").length;
  const newCert = { name: "", domains: [], challenge: "http-01", dns_provider: "", dns_opts: {}, storage_dir: "", deploys: [] };
  wrap.insertAdjacentHTML("beforeend", certCardHtml(newCert, idx));
  if (configState) configState.certificates.push(newCert);
}

// 配置表单动态增删与 SSH 测试(事件委托到 view-config)
document.getElementById("view-config").addEventListener("click", async (e) => {
  const t = e.target;
  // 折叠/展开证书卡片(状态按证书名持久化)
  if (t.dataset.toggleCert !== undefined) {
    const card = t.closest(".cert-card");
    card.classList.toggle("collapsed");
    const name = card.querySelector('[name="name"]')?.value ?? "";
    certSetCollapsed(name, card.classList.contains("collapsed"));
    return;
  }
  // 新增证书条目(按钮在证书配置内容区)
  if (t.id === "btn-add-cert") {
    addCertCard();
    return;
  }
  // 新增 SSH 主机
  if (t.id === "btn-add-host") {
    const wrap = document.getElementById("cfg-hosts");
    const idx = document.querySelectorAll(".host-card").length;
    wrap.insertAdjacentHTML(
      "beforeend",
      `<div class="host-card" data-host="${idx}">
        <div class="grid">
          ${field("主机名", "host_name", "", { placeholder: "prod-server" })}
          ${field("主机", "host_host", "")}
          ${field("端口", "host_port", 22)}
          ${field("用户", "host_user", "")}
          ${field("私钥路径", "host_key", "", { placeholder: "~/.ssh/id_ed25519" })}
          ${field("远程目录", "host_remote_path", "")}
          ${field("reload 命令", "host_reload_cmd", "", { placeholder: "nginx -t && nginx -s reload" })}
        </div>
        <div class="host-actions">
          ${sshTestBtn(`host:${""}`)}
          <button class="btn danger small" data-rm-host="${idx}">删除该主机</button>
        </div>
      </div>`
    );
    return;
  }
  // 删除主机(二次确认;移除仅改内存表单,点「保存配置」后才真正生效)
  if (t.dataset.rmHost !== undefined) {
    const card = t.closest(".host-card");
    const name = card?.querySelector('[name="host_name"]')?.value || "";
    const refs = document.querySelectorAll('.deploy-card select[name="host_ref"]');
    const used = [...refs].some((s) => s.value === name);
    const msg = `确定删除 SSH 主机${name ? `「${name}」` : ""}吗?\n` +
      (used ? "⚠️ 有证书部署目标引用了该主机,删除后其引用将失效。\n" : "") +
      "删除后需点击「保存配置」才会真正生效。";
    if (!confirm(msg)) return;
    card.remove();
    return;
  }
  // 删除证书条目(二次确认;移除仅改内存表单,点「保存配置」后才真正生效)
  if (t.dataset.rmCert !== undefined) {
    const card = t.closest(".cert-card");
    const name = card?.querySelector('[name="name"]')?.value || "";
    if (!confirm(`确定删除证书条目${name ? `「${name}」` : ""}吗?\n删除后需点击「保存配置」才会真正生效。`)) return;
    card.remove();
    return;
  }
  // DNS 密钥增删
  if (t.dataset.rmDns !== undefined) {
    t.closest(".dns-row").remove();
    // 删除最后一个密钥后,重新显示「添加密钥」按钮(密钥一个就够)
    const card = t.closest(".cert-card");
    if (card && card.querySelectorAll(".dns-row.dns-opt-row").length === 0
      && card.querySelectorAll('[name="dns_key"]').length === 0) {
      const section = card.querySelectorAll(".form-section")[0];
      const addBtn = section.querySelector('[data-add-dns]');
      if (!addBtn) {
        section.insertAdjacentHTML("beforeend", `<button class="btn ghost small section-add-btn" data-add-dns="${card.dataset.cert}">添加密钥</button>`);
      }
    }
    return;
  }
  if (t.dataset.addDns !== undefined) {
    const card = t.closest(".cert-card");
    const section = card.querySelectorAll(".form-section")[0];
    section.insertAdjacentHTML(
      "beforeend",
      `<div class="grid dns-row">
        ${field("密钥名(环境变量名)", "dns_key", "", { placeholder: "DNSPOD_API_KEY" })}
        ${field("密钥值(明文自动保存为环境变量)", "dns_val", "", { placeholder: "ID,Token" })}
        <div class="field"><label>&nbsp;</label><button class="btn danger small" data-rm-dns="-1">删除</button></div>
      </div>`
    );
    // 已有密钥后隐藏「添加密钥」按钮
    const addBtn = section.querySelector('[data-add-dns]');
    if (addBtn) addBtn.remove();
    return;
  }
  // 部署增删
  if (t.dataset.addDeploy !== undefined) {
    const card = t.closest(".cert-card");
    const section = card.querySelectorAll(".form-section")[1];
    const di = card.querySelectorAll(".deploy-card").length;
    const certName = card.querySelector('[name="name"]')?.value ?? "";
    section.insertAdjacentHTML("beforeend", deployFieldsHtml({ type: "nginx" }, di, certName));
    return;
  }
  if (t.dataset.rmDeploy !== undefined) {
    t.closest(".deploy-card").remove();
    return;
  }
  // 测试 SSH 连接(hosts 区)
  if (t.closest("[data-test-ssh-key]") && t.closest(".host-card")) {
    const card = t.closest(".host-card");
    const hg = (n) => card.querySelector(`[name="${n}"]`)?.value ?? "";
    const hostName = hg("host_name");
    const btn = t.closest("[data-test-ssh-key]");
    // 以当前表单主机名为 key(空名主机用占位,测试成功后按实际名重写持久化)
    btn.dataset.testSshKey = `host:${hostName}`;
    await testSSH({
      host: hg("host_host"),
      port: parseInt(hg("host_port") || "22", 10),
      user: hg("host_user"),
      key: hg("host_key"),
    }, btn);
    return;
  }
  // 测试 SSH 连接(部署卡片)
  if (t.closest("[data-test-ssh-key]") && t.closest(".deploy-card")) {
    const card = t.closest(".deploy-card");
    const dg = (n) => card.querySelector(`[name="${n}"]`)?.value ?? "";
    const certCard = t.closest(".cert-card");
    const certName = certCard?.querySelector('[name="name"]')?.value ?? "";
    const btn = t.closest("[data-test-ssh-key]");
    const params = {
      host_ref: dg("host_ref"),
      host: dg("host"),
      port: parseInt(dg("port") || "0", 10),
      user: dg("user"),
      key: dg("ssh_key"),
    };
    btn.dataset.testSshKey = sshDeployKey(certName, params);
    await testSSH(params, btn);
  }
});

// SSH 测试连接按钮:测试通过后持久化为绿色 ok 态(除非再次测试失败恢复默认)
const SSH_OK_KEY = "gozs-ssh-ok";
const CERT_COLLAPSE_KEY = "gozs-cert-collapsed";

function certCollapsedMap() {
  try { return JSON.parse(localStorage.getItem(CERT_COLLAPSE_KEY) || "{}"); } catch (e) { return {}; }
}
function certIsCollapsed(name) {
  return !!certCollapsedMap()[name];
}
function certSetCollapsed(name, collapsed) {
  const m = certCollapsedMap();
  if (collapsed) m[name] = true; else delete m[name];
  try { localStorage.setItem(CERT_COLLAPSE_KEY, JSON.stringify(m)); } catch (e) { /* 忽略 */ }
}

function sshOkMap() {
  try { return JSON.parse(localStorage.getItem(SSH_OK_KEY) || "{}"); } catch (e) { return {}; }
}
function sshIsOk(key) {
  return !!sshOkMap()[key];
}
function sshSetOk(key, ok) {
  const m = sshOkMap();
  if (ok) m[key] = true; else delete m[key];
  try { localStorage.setItem(SSH_OK_KEY, JSON.stringify(m)); } catch (e) { /* 忽略 */ }
}
// key 形如 "host:prod-server" / "deploy:example:host@22";证书名变化或主机信息变化后自然失效
function sshTestBtn(key) {
  const ok = sshIsOk(key);
  return `<button class="btn small ${ok ? "ok" : "ghost"}" data-test-ssh-key="${esc(key)}">测试连接${ok ? " ✓" : ""}</button>`;
}
// SSH 部署目标:引用主机时用 host_ref,否则用 host@port 作为稳定标识
function sshDeployKey(certName, d) {
  const ref = d.host_ref || "";
  const host = d.host || "";
  const port = d.port || 22;
  const ident = ref || `${host}@${port}`;
  return `deploy:${certName || ""}:${ident}`;
}

async function testSSH(params, btn) {
  btn.disabled = true;
  const orig = btn.textContent;
  btn.textContent = "测试中…";
  const key = btn.dataset.testSshKey || "";
  try {
    const msg = await go().TestSSH(params);
    // 测试通过:按钮转绿色并持久化,保留 ✓ 文字
    btn.classList.remove("ghost");
    btn.classList.add("ok");
    btn.textContent = "测试连接 ✓";
    if (key) sshSetOk(key, true);
    toast(msg);
  } catch (err) {
    // 测试失败:回到默认状态并清除持久化记录
    btn.classList.remove("ok");
    btn.classList.add("ghost");
    btn.textContent = "测试连接";
    if (key) sshSetOk(key, false);
    toast("连接失败: " + err, false);
  } finally {
    // 失败时恢复原始文字;成功时保留 ✓ 显示
    if (!btn.classList.contains("ok")) btn.textContent = orig;
    btn.disabled = false;
  }
}

// 部署类型切换:重建该卡片以显示对应类型的字段
document.getElementById("view-config").addEventListener("change", (e) => {
  if (e.target.matches('.deploy-card select[name="type"]')) {
    const card = e.target.closest(".deploy-card");
    const d = collectDeployCard(card);
    d.type = e.target.value;
    // 清空不属于新类型的字段,避免旧类型残留值被带过去
    clearForeignFields(d, d.type);
    const idx = parseInt(card.dataset.deploy || "0", 10);
    const certName = card.closest(".cert-card")?.querySelector('[name="name"]')?.value ?? "";
    const tmp = document.createElement("div");
    tmp.innerHTML = deployFieldsHtml(d, idx, certName);
    card.replaceWith(tmp.firstElementChild);
    return;
  }
  // host_ref 切换:有引用则隐藏内联字段,无引用则显示
  if (e.target.matches('.deploy-card select[name="host_ref"]')) {
    const card = e.target.closest(".deploy-card");
    if (card) {
      card.querySelectorAll(".ssh-inline").forEach((f) => {
        f.style.display = e.target.value ? "none" : "";
      });
    }
    return;
  }
  // dns 服务商切换:重建证书卡片以显示/隐藏 dnspod 明文参数
  if (e.target.matches('.cert-card select[name="dns_provider"]')) {
    const card = e.target.closest(".cert-card");
    const idx = parseInt(card.dataset.cert || "0", 10);
    const c = collectCertCard(card);
    const tmp = document.createElement("div");
    tmp.innerHTML = certCardHtml(c, idx);
    card.replaceWith(tmp.firstElementChild);
  }
});

// 收集单个证书卡片的值(供 dns_provider 切换重建用)
function collectCertCard(card) {
  const get = (n) => card.querySelector(`[name="${n}"]`)?.value ?? "";
  const cert = {
    name: get("name"),
    domains: (get("domains") || "").split(",").map((s) => s.trim()).filter(Boolean),
    challenge: get("challenge"),
    dns_provider: get("dns_provider"),
    storage_dir: get("storage_dir"),
    dns_opts: {},
    deploys: [],
  };
  const dnsKeys = card.querySelectorAll('[name="dns_key"]');
  const dnsVals = card.querySelectorAll('[name="dns_val"]');
  DNS_OPT_META.forEach((m) => {
    const v = (card.querySelector(`[name="${m.name}"]`)?.value ?? "").trim();
    if (v) cert.dns_opts[m.key] = v;
  });
  dnsKeys.forEach((k, i) => {
    const key = k.value.trim();
    if (key && !isNonSecretDnsKey(key)) cert.dns_opts[key] = dnsVals[i]?.value ?? "";
  });
  card.querySelectorAll(".deploy-card").forEach((dc) => {
    const d = collectDeployCard(dc);
    // 修正:host_ref 已选时,丢弃 ssh 内联字段(避免隐藏字段旧值残留)
    if (d.type === "ssh" && d.host_ref) {
      d.host = "";
      d.port = 0;
      d.user = "";
      d.ssh_key = "";
      d.remote_path = "";
      d.reload_cmd = "";
    }
    cert.deploys.push(d);
  });
  return cert;
}

// 收集单个部署卡片的值(供类型切换重建用)
function collectDeployCard(card) {
  const g = (n) => card.querySelector(`[name="${n}"]`)?.value ?? "";
  return {
    type: g("type"),
    reload_cmd: g("reload_cmd"),
    test_cmd: g("test_cmd"),
    cert_path: g("cert_path"),
    key_path: g("key_path"),
    host: g("host"),
    host_ref: g("host_ref"),
    port: parseInt(g("port") || "0", 10),
    user: g("user"),
    ssh_key: g("ssh_key"),
    remote_path: g("remote_path"),
    cert_filename: g("cert_filename"),
    key_filename: g("key_filename"),
    dir: g("dir"),
    url: g("url"),
  };
}

// 类型专属字段:切换类型时清空不属于该类型的字段
const TYPE_FIELDS = {
  nginx: ["reload_cmd", "test_cmd", "cert_path", "key_path"],
  ssh: ["host_ref", "host", "port", "user", "ssh_key", "remote_path", "reload_cmd", "cert_filename", "key_filename"],
  file: ["dir"],
  webhook: ["url"],
};

function clearForeignFields(d, type) {
  const own = TYPE_FIELDS[type] || [];
  const all = ["reload_cmd", "test_cmd", "cert_path", "key_path", "host_ref", "host", "port", "user", "ssh_key", "remote_path", "cert_filename", "key_filename", "dir", "url"];
  all.forEach((f) => {
    if (!own.includes(f)) d[f] = f === "port" ? 0 : "";
  });
}

// ---------- 通知页 ----------
async function loadNotifyForm() {
  try {
    const cfg = await go().GetConfig();
    renderNotifyForm(cfg);
  } catch (e) {
    toast("加载通知设置失败: " + (e && e.message ? e.message : e), false);
    console.error("[easyssh] GetConfig(通知) 失败:", e);
  }
}

function renderNotifyForm(cfg) {
  document.getElementById("notify-form").innerHTML = `
    <div class="form-section">
      <h3>触发事件</h3>
      <div class="switch-group">
        ${switchField("即将到期提醒", "notify_expiring", cfg.notify_expiring, "证书进入续期窗口时推送,附到期时间(24 小时内最多提醒一次)")}
        ${switchField("续期/部署成功提醒", "notify_success", cfg.notify_success, "签发/续期/部署成功后推送,附新到期时间")}
      </div>
      <div class="muted" style="margin-top:10px">证书签发/续期/部署失败的告警始终推送,不受开关控制。</div>
    </div>
    <div class="form-section">
      <h3>邮件(SMTP)</h3>
      <div class="grid">
        ${field("SMTP 服务器", "smtp_host", cfg.smtp_host, { placeholder: "smtp.qq.com" })}
        ${field("SMTP 端口", "smtp_port", cfg.smtp_port ?? 465)}
        ${field("SMTP 账号", "smtp_user", cfg.smtp_user, { placeholder: "you@qq.com" })}
        ${field("SMTP 授权码(明文自动存为环境变量)", "smtp_pass", cfg.smtp_pass, { placeholder: "授权码或 {{env:VAR}}" })}
      </div>
      <div class="field" style="margin-top:12px">
        <label>收件人(每行一个,支持多个)</label>
        <textarea name="smtp_to" rows="3" placeholder="ops@example.com">${esc((cfg.smtp_to || []).join("\n"))}</textarea>
      </div>
    </div>
    <div class="form-section">
      <h3>Webhook</h3>
      <div class="field">
        <label>推送 URL(可选,收到事件 JSON)</label>
        <input name="webhook" value="${esc(cfg.webhook || "")}" placeholder="https://ops.example.com/alert" />
      </div>
    </div>`;
}

// 测试通知发送:先保存当前表单(保证用最新 SMTP 配置),再发送固定测试邮件。
// 成功提示已发出、请查收;失败展示具体错误。
document.getElementById("btn-test-notify").addEventListener("click", async () => {
  const btn = document.getElementById("btn-test-notify");
  btn.disabled = true;
  const orig = btn.textContent;
  btn.textContent = "发送中…";
  try {
    const base = await go().GetConfig();
    base.notify_expiring = document.querySelector('[name="notify_expiring"]')?.checked ?? false;
    base.notify_success = document.querySelector('[name="notify_success"]')?.checked ?? false;
    base.smtp_host = qv("smtp_host");
    base.smtp_port = parseInt(qv("smtp_port") || "465", 10);
    base.smtp_user = qv("smtp_user");
    base.smtp_pass = qv("smtp_pass");
    base.smtp_to = (qv("smtp_to") || "").split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
    base.webhook = qv("webhook");
    await go().SaveConfig(base);
    toast(await go().TestNotify());
  } catch (err) {
    toast("测试发送失败: " + err, false);
  } finally {
    btn.disabled = false;
    btn.textContent = orig;
  }
});

// 保存通知:基于后端当前配置,仅覆盖通知相关字段
document.getElementById("btn-save-notify").addEventListener("click", async () => {
  const btn = document.getElementById("btn-save-notify");
  btn.disabled = true;
  try {
    const base = await go().GetConfig();
    base.notify_expiring = document.querySelector('[name="notify_expiring"]')?.checked ?? false;
    base.notify_success = document.querySelector('[name="notify_success"]')?.checked ?? false;
    base.smtp_host = qv("smtp_host");
    base.smtp_port = parseInt(qv("smtp_port") || "465", 10);
    base.smtp_user = qv("smtp_user");
    base.smtp_pass = qv("smtp_pass");
    base.smtp_to = (qv("smtp_to") || "").split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
    base.webhook = qv("webhook");
    const msg = await go().SaveConfig(base);
    toast(msg);
    await refresh();
  } catch (err) {
    toast("保存失败: " + err, false);
  } finally {
    btn.disabled = false;
  }
});

// ---------- 主题切换 ----------
const themeBtn = document.getElementById("btn-theme");
const iconSun = document.getElementById("icon-sun");
const iconMoon = document.getElementById("icon-moon");
function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  // 暗色主题显示月亮图标(点击切到亮色),反之亦然
  const dark = theme === "dark";
  iconMoon.classList.toggle("hidden", !dark);
  iconSun.classList.toggle("hidden", dark);
  try { localStorage.setItem("gozs-theme", theme); } catch (e) { /* 忽略 */ }
}
try {
  applyTheme(localStorage.getItem("gozs-theme") || "dark");
} catch (e) {
  applyTheme("dark");
}
themeBtn.addEventListener("click", () => {
  const cur = document.documentElement.dataset.theme === "light" ? "dark" : "light";
  applyTheme(cur);
});

// ---------- 日志 ----------
async function refreshLogs(force = false) {
  try {
    const entries = await go().GetLogs(200);
    const box = document.getElementById("log-list");
    if (!force && box._count === entries.length && box._last === (entries[entries.length - 1]?.msg ?? "")) {
      return; // 无新日志
    }
    box._count = entries.length;
    box._last = entries[entries.length - 1]?.msg ?? "";
    if (entries.length === 0) {
      box.innerHTML = `<div class="log-empty">暂无日志</div>`;
      return;
    }
    box.innerHTML = entries
      .map((l) => {
        const t = new Date(l.time).toLocaleTimeString("zh-CN", { hour12: false });
        return `<div class="log-line"><span class="log-time">${esc(t)}</span><span class="log-msg">${esc(l.msg)}</span></div>`;
      })
      .join("");
    box.scrollTop = box.scrollHeight;
  } catch (e) {
    /* 忽略日志加载错误 */
  }
}

document.getElementById("btn-clear-logs").addEventListener("click", () => {
  const box = document.getElementById("log-list");
  box.innerHTML = `<div class="log-empty">已清空显示(新日志仍会追加)</div>`;
  box._count = -1;
});

// 复制日志到剪贴板(供反馈/排查)
document.getElementById("btn-copy-logs").addEventListener("click", async () => {
  try {
    const entries = await go().GetLogs(500);
    const text = entries
      .map((l) => new Date(l.time).toLocaleString("zh-CN", { hour12: false }) + " " + l.msg)
      .join("\n");
    await navigator.clipboard.writeText(text || "(无日志)");
    toast("日志已复制到剪贴板(" + entries.length + " 条)");
  } catch (e) {
    toast("复制失败: " + e, false);
  }
});

document.getElementById("btn-scan").addEventListener("click", async () => {
  const btn = document.getElementById("btn-scan");
  btn.disabled = true;
  const orig = btn.textContent;
  btn.textContent = "检查中…";
  try {
    toast(await go().RunCheck());
    await refresh();
  } catch (err) {
    toast(String(err), false);
  } finally {
    btn.disabled = false;
    btn.textContent = orig;
  }
});

document.getElementById("btn-reload").addEventListener("click", async () => {
  try {
    toast(await go().ReloadConfig());
    await refresh();
  } catch (err) {
    toast(String(err), false);
  }
});

// 重建并重启:后端执行 wails build → 生成重启脚本,成功后本进程退出,脚本用新 exe 重启
document.getElementById("btn-rebuild").addEventListener("click", async () => {
  const btn = document.getElementById("btn-rebuild");
  if (!confirm("将重新构建 GUI(wails build)并自动重启应用。\n构建期间界面会卡住数秒,重启后自动生效。\n\n确定继续吗?")) return;
  btn.disabled = true;
  const orig = btn.textContent;
  btn.textContent = "构建中…";
  try {
    const msg = await go().RebuildAndRestart();
    toast(msg);
    // 给 toast 一点展示时间后退出,重启脚本接管
    setTimeout(() => {
      try { window.runtime.Quit(); } catch (e) { /* 忽略 */ }
    }, 1500);
  } catch (err) {
    toast("重建失败: " + err, false);
    btn.disabled = false;
    btn.textContent = orig;
  }
});

// 初始化:概览 + 自动刷新(证书 30s,日志 3s)
refresh();
setInterval(refresh, 30000);
setInterval(() => refreshLogs(), 3000);

// ---------- 导出/导入 bundle ----------
// 导出:模态框勾选范围(config 始终包含)→ 需口令时填口令 → 保存文件
const exportModal = document.getElementById("export-modal");
const expConfig = document.getElementById("exp-config");
const expSecrets = document.getElementById("exp-secrets");
const expCerts = document.getElementById("exp-certs");
const expSSH = document.getElementById("exp-ssh");
const expPassWrap = document.getElementById("exp-pass-wrap");
const expPass = document.getElementById("exp-pass");

function updateExportPassVisibility() {
  const need = expSecrets.checked || expSSH.checked;
  expPassWrap.classList.toggle("hidden", !need);
  if (!need) expPass.value = "";
}

function openExportModal() {
  expConfig.checked = true;
  expSecrets.checked = false;
  expCerts.checked = false;
  expSSH.checked = false;
  expPass.value = "";
  updateExportPassVisibility();
  exportModal.classList.remove("hidden");
}

function closeExportModal() {
  exportModal.classList.add("hidden");
  expPass.value = "";
}

document.getElementById("btn-export-bundle").addEventListener("click", openExportModal);
expSecrets.addEventListener("change", updateExportPassVisibility);
expSSH.addEventListener("change", updateExportPassVisibility);
exportModal.querySelectorAll("[data-close-export]").forEach((el) =>
  el.addEventListener("click", closeExportModal)
);
// 点遮罩关闭
exportModal.addEventListener("click", (e) => {
  if (e.target === exportModal) closeExportModal();
});

document.getElementById("btn-confirm-export").addEventListener("click", async () => {
  const scope = {
    config: true,
    secrets: expSecrets.checked,
    certs: expCerts.checked,
    ssh_keys: expSSH.checked,
  };
  const needsPass = scope.secrets || scope.ssh_keys;
  const password = expPass.value.trim();
  if (needsPass && (!password || password.length < 10 || /^\d+$/.test(password))) {
    toast("口令过弱:至少 10 字符且非纯数字", false);
    return;
  }
  // 系统保存对话框(Go 侧 SelectSavePath;取消返回空串)
  let outPath = "";
  try {
    outPath = await go().SelectSavePath({
      DefaultFilename: "easyssh-export.zsbundle",
      Filters: [{ DisplayName: "easyssh 导出包", Pattern: "*.zsbundle" }],
    });
  } catch (e) { outPath = ""; }
  if (!outPath) {
    toast("已取消导出", false);
    return;
  }
  try {
    const res = await go().ExportBundle({ out_path: outPath, scope, password });
    closeExportModal();
    toast("已导出到 " + res.out_path);
  } catch (err) {
    toast("导出失败: " + String(err), false);
  }
});

// 导入:选文件 → 输口令(必要时)→ 预览 → 确认
document.getElementById("btn-import-bundle").addEventListener("click", async () => {
  let filePath = "";
  try {
    filePath = await go().SelectOpenPath({
      Filters: [{ DisplayName: "easyssh 导出包", Pattern: "*.zsbundle" }],
    });
  } catch (e) { filePath = ""; }
  if (!filePath) {
    toast("已取消导入", false);
    return;
  }
  // 先预览(无口令,若有加密段后端会返回 needs_pass)
  let pv;
  try {
    pv = await go().PreviewImportBundle({ file_path: filePath, password: "" });
  } catch (err) {
    toast("读取导出包失败: " + String(err), false);
    return;
  }
  let password = "";
  if (pv.needs_pass) {
    password = prompt("该导出包含加密的密钥/私钥,请输入导入口令:");
    if (password === null) return;
  }
  // 重新读取(带口令,验证正确性)
  try {
    pv = await go().PreviewImportBundle({ file_path: filePath, password });
  } catch (err) {
    toast("口令错误或包损坏: " + String(err), false);
    return;
  }
  const certs = (pv.cert_names || []).join(", ") || "(无)";
  const envs = (pv.env_secrets || []).join(", ") || "(无)";
  const keys = (pv.ssh_key_paths || []).join(", ") || "(无)";
  const conflict = prompt(
    `导出包预览:\n  导出时间: ${pv.exported_at || "-"}\n  范围: config${pv.has_secrets ? "+密钥" : ""}${pv.has_certs ? "+证书产物" : ""}${pv.has_ssh_keys ? "+SSH私钥" : ""}\n  证书条目: ${certs}\n  密钥变量: ${envs}\n  SSH 私钥: ${keys}\n\n冲突处理策略(append=改名追加 / skip=跳过 / overwrite=覆盖):`,
    "append"
  );
  if (conflict === null) return;
  const c = conflict.trim().toLowerCase();
  if (!["append", "skip", "overwrite"].includes(c)) {
    toast("非法冲突策略: " + conflict, false);
    return;
  }
  try {
    const msg = await go().ImportBundle({ file_path: filePath, password, conflict: c });
    toast(msg);
    await refresh();
  } catch (err) {
    toast("导入失败: " + String(err), false);
  }
});
