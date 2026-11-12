const CMD = {
  NOOP: 6,
  AUTH: 800,
  KICK: 802,
  ENTER: 1001,
  STATUS: 1004,
  SEND: 1050,
  ROOM_KICKED: 1060,
  MESSAGE: 1070,
};
const CLIENT_VERSION = 100000000;
const CLIENT_WEB = 5;

const $ = (id) => document.getElementById(id);
let root;
let seq = 0;
let ws;
let pending = new Map();
let me = null;
const names = new Map();

async function loadProto() {
  const desc = await (await fetch("client.json")).json();
  root = protobuf.Root.fromJSON(desc);
}

function type(name) {
  return root.lookupType(name);
}

function encode(name, value) {
  const t = type(name);
  return t.encode(t.create(value)).finish();
}

function decode(name, bytes) {
  return type(name).decode(bytes);
}

function packet(cmd, seqNo, body) {
  const out = new Uint8Array(20 + body.length);
  const view = new DataView(out.buffer);
  view.setUint32(0, 20, false);
  view.setUint32(4, CLIENT_VERSION, false);
  view.setUint32(8, cmd, false);
  view.setUint32(12, seqNo, false);
  view.setUint32(16, body.length, false);
  out.set(body, 20);
  return out;
}

function readPacket(buffer) {
  const bytes = new Uint8Array(buffer);
  if (bytes.length < 20) return null;
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const bodyLen = view.getUint32(16, false);
  return {
    cmd: view.getUint32(8, false),
    seq: view.getUint32(12, false),
    body: bytes.slice(20, 20 + bodyLen),
  };
}

function request(cmd, body) {
  const seqNo = ++seq;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(seqNo);
      reject(new Error("网关没有在 8 秒内响应"));
    }, 8000);
    pending.set(seqNo, { resolve, reject, timer });
    ws.send(packet(cmd, seqNo, body));
  });
}

function retOK(ret) {
  return ret && (ret.errorCode === 0 || ret.errorCode === "SUCCESS");
}

function connect(url) {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(url);
    socket.binaryType = "arraybuffer";
    socket.onopen = () => resolve(socket);
    socket.onerror = () => reject(new Error("连不上网关 " + url));
  });
}

const ROOM_EVENT = {
  1: "进入了房间",
  2: "离开了房间",
  19: "上线了",
  20: "下线了",
  CHATROOM_EVENTTYPE_ENTER: "进入了房间",
  CHATROOM_EVENTTYPE_EXIT: "离开了房间",
  CHATROOM_EVENTTYPE_USERONLINE: "上线了",
  CHATROOM_EVENTTYPE_USEROFFLINE: "下线了",
};

function roomNotice(msg) {
  const room = msg.notification && msg.notification.chatroomNotificationContent;
  const source = room && room.source;
  if (source && source.userId && source.roomNickname) names.set(String(source.userId), source.roomNickname);
  const who = source && (source.roomNickname || source.userId);
  const action = (room && ROOM_EVENT[room.eventType]) || "有一条房间通知";
  return who ? who + " " + action : action;
}

function senderName(uid) {
  if (String(uid) === String(me && me.uid)) return "我";
  return names.get(String(uid)) || "用户 " + uid;
}

function onPacket(p) {
  const waiter = pending.get(p.seq);
  if (waiter) {
    pending.delete(p.seq);
    clearTimeout(waiter.timer);
    waiter.resolve(p);
    return;
  }
  if (p.cmd === CMD.MESSAGE) {
    const msg = decode("orbikit.client.message.Message", p.body);
    if (!msg) return;
    if (msg.messageType === 3) {
      addLine("system", "", roomNotice(msg));
      return;
    }
    const mine = String(msg.from) === String(me.uid);
    addLine(mine ? "mine" : "", senderName(msg.from), msg.text || "[非文本消息]");
  } else if (p.cmd === CMD.KICK) {
    setStatus("已在其他地方登录，本页已断开");
  } else if (p.cmd === CMD.ROOM_KICKED) {
    addLine("system", "", "你被移出了房间");
  }
}

function addLine(kind, who, text) {
  const li = document.createElement("li");
  if (kind) li.className = kind;
  if (who) {
    const name = document.createElement("span");
    name.className = "who";
    name.textContent = who;
    li.appendChild(name);
  }
  li.appendChild(document.createTextNode(text));
  const log = $("log");
  log.appendChild(li);
  log.scrollTop = log.scrollHeight;
}

function setStatus(text) {
  $("status").textContent = text;
}

async function joinRoom() {
  const button = $("join");
  button.disabled = true;
  $("lobby-error").textContent = "";
  try {
    const resp = await fetch("/api/join", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        nickname: $("nick").value,
        room_id: Number($("room").value || 1001),
      }),
    });
    const data = await resp.json();
    if (!resp.ok) throw new Error(data.error || "进入失败");
    me = data;

    ws = await connect(data.ws_url);
    ws.onmessage = (ev) => {
      const p = readPacket(ev.data);
      if (p) onPacket(p);
    };
    ws.onclose = () => setStatus("连接已断开");

    const auth = await request(CMD.AUTH, encode("orbikit.client.AuthReq", {
      appId: data.app_id,
      authToken: data.login_token,
      deviceId: "web-" + data.uid,
      type: CLIENT_WEB,
    }));
    const authResp = decode("orbikit.client.AuthResp", auth.body);
    if (!retOK(authResp.ret)) throw new Error("登录被拒绝：" + (authResp.ret && authResp.ret.errorMsg));

    const entered = await request(CMD.ENTER, encode("orbikit.client.chatroom.EnterChatroomReq", {
      roomId: data.room_id,
      token: data.room_token,
    }));
    const enterResp = decode("orbikit.client.chatroom.EnterChatroomResp", entered.body);
    if (!retOK(enterResp.ret)) throw new Error("进房被拒绝：" + (enterResp.ret && enterResp.ret.errorMsg));

    $("room-label").textContent = data.room_id;
    setStatus(data.nickname + " · 已在房间 " + data.room_id);
    $("lobby").hidden = true;
    $("chat").hidden = false;
    addLine("system", "", "你进入了房间");
    $("text").focus();
  } catch (err) {
    $("lobby-error").textContent = err.message;
    if (ws) ws.close();
  } finally {
    button.disabled = false;
  }
}

$("join").addEventListener("click", joinRoom);
$("nick").addEventListener("keydown", (ev) => {
  if (ev.key === "Enter") joinRoom();
});

$("composer").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const text = $("text").value.trim();
  if (!text || !ws) return;
  $("text").value = "";
  try {
    const reply = await request(CMD.SEND, encode("orbikit.client.message.SendMessageReq", {
      message: {
        messageType: 1,
        text,
        session: { sessionId: me.room_id, sessionType: 1 },
        messageId: crypto.randomUUID(),
        senderClientType: CLIENT_WEB,
      },
    }));
    const resp = decode("orbikit.client.message.SendMessageResp", reply.body);
    if (!retOK(resp.ret)) addLine("system", "", "发送失败：" + (resp.ret && resp.ret.errorMsg));
    else addLine("mine", "我", text);
  } catch (err) {
    addLine("system", "", err.message);
  }
});

loadProto().catch((err) => {
  $("lobby-error").textContent = "协议描述加载失败：" + err.message;
});
