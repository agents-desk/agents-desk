/*
 * agents-desk 网页聊天插件的嵌入脚本，放在产品网站的每个页面中：
 *
 *   <script>window.agentsDesk = { channel: "<聊天插件>", user_token: "<已登录用户的身份，可选>" };</script>
 *   <script async src="https://<agents-desk 的地址>/widget/loader.js"></script>
 *
 * 它在页面右下角放一个 iframe，里面是聊天窗口（/widget/?channel=…&embed=1），按聊天窗口发来的消息调整 iframe 的大小：
 * 收起时只有启动按钮；电脑上展开后是浮层；手机的宽度（≤ 480px）展开后占满屏幕，并锁住页面的滚动。
 * 页面通过 window.AgentsDesk(命令) 控制聊天窗口：open、close、login（{ user_token }）、logout。
 * user_token 通过 postMessage 交给聊天窗口，不放进地址。脚本不依赖任何库，可以在任何网站上运行。
 */
(function () {
  "use strict";
  var config = window.agentsDesk || {};
  if (!config.channel || window.__agentsDeskLoaded) return;
  window.__agentsDeskLoaded = true;

  var PREFIX = "agents-desk:";
  var script = document.currentScript;
  var origin = new URL(script ? script.src : "/widget/loader.js", location.href).origin;
  var mobileQuery = window.matchMedia("(max-width: 480px)");
  var state = { ready: false, open: false, queue: [] };

  // iframe：透明、没有边框，固定在右下角
  var frame = document.createElement("iframe");
  frame.src = origin + "/widget/?channel=" + encodeURIComponent(config.channel) + "&embed=1";
  frame.title = "Chat";
  frame.setAttribute("allow", "clipboard-write");
  frame.setAttribute("allowtransparency", "true");
  // color-scheme 与聊天窗口的页面（浅色）相同，浏览器才把 iframe 画成透明
  var base = "position:fixed;border:0;background:transparent;color-scheme:light;z-index:2147483000;";

  // 页面原来的滚动设置，手机上全屏时锁住，收起后恢复
  var savedOverflow = null;

  // layout 按开合和宽度设置 iframe 的大小
  function layout() {
    var mobile = mobileQuery.matches;
    if (state.open && mobile) {
      frame.style.cssText = base + "inset:0;width:100%;height:100%;";
      if (savedOverflow === null) {
        savedOverflow = [document.documentElement.style.overflow, document.body.style.overflow];
        document.documentElement.style.overflow = "hidden";
        document.body.style.overflow = "hidden";
      }
    } else {
      if (state.open) {
        frame.style.cssText = base + "right:0;bottom:0;width:412px;height:min(720px,100%);";
      } else {
        frame.style.cssText = base + "right:0;bottom:" + (mobile ? "12px" : "0") + ";width:88px;height:88px;";
      }
      if (savedOverflow !== null) {
        document.documentElement.style.overflow = savedOverflow[0];
        document.body.style.overflow = savedOverflow[1];
        savedOverflow = null;
      }
    }
  }

  // post 发消息给聊天窗口，只发给 agents-desk 的地址；聊天窗口还没准备好时先排队
  function post(message) {
    if (!state.ready) {
      state.queue.push(message);
      return;
    }
    frame.contentWindow.postMessage(message, origin);
  }

  function sendLayout() {
    post({ type: PREFIX + "layout", mobile: mobileQuery.matches });
  }

  // 聊天窗口发来的消息：只接受这个 iframe、来自 agents-desk 的地址
  window.addEventListener("message", function (e) {
    if (e.source !== frame.contentWindow || e.origin !== origin || !e.data || typeof e.data.type !== "string") return;
    var type = e.data.type.slice(PREFIX.length);
    if (type === "ready") {
      state.ready = true;
      frame.contentWindow.postMessage({ type: PREFIX + "init", user_token: config.user_token || "", mobile: mobileQuery.matches }, origin);
      var queued = state.queue;
      state.queue = [];
      queued.forEach(post);
    } else if (type === "open" || type === "close") {
      state.open = type === "open";
      layout();
    }
  });

  if (mobileQuery.addEventListener) {
    mobileQuery.addEventListener("change", function () {
      layout();
      sendLayout();
    });
  }

  // 页面上的命令
  window.AgentsDesk = function (command, arg) {
    if (command === "open" || command === "close" || command === "logout") {
      if (command === "logout") delete config.user_token;
      post({ type: PREFIX + "command", command: command });
    } else if (command === "login" && arg && arg.user_token) {
      config.user_token = arg.user_token;
      post({ type: PREFIX + "command", command: "login", user_token: arg.user_token });
    }
  };

  function mount() {
    layout();
    document.body.appendChild(frame);
  }
  if (document.body) mount();
  else document.addEventListener("DOMContentLoaded", mount);
})();
