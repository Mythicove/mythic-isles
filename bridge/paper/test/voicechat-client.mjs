// Protocol-only Simple Voice Chat 2.6.x fixture (compatibility 20). Runs against
// the real plugin; no API setters, injected plugin state or audio capture.
import { createSocket } from "node:dgram";
import { createCipheriv, createDecipheriv, randomBytes } from "node:crypto";
import assert from "node:assert/strict";

function varint(value) {
  const bytes = [];
  do { let byte = value & 127; value >>>= 7; if (value) byte |= 128; bytes.push(byte); } while (value);
  return Buffer.from(bytes);
}

export async function connectVoicechat(bot) {
  const client = bot._client;
  const secretPacket = new Promise((resolve, reject) => {
    const onPayload = ({ channel, data }) => {
      if (channel !== "voicechat:secret") return;
      clearTimeout(timer);
      client.off("custom_payload", onPayload);
      resolve(data);
    };
    const timer = setTimeout(() => {
      client.off("custom_payload", onPayload);
      reject(new Error("Voice secret handshake timed out"));
    }, 15_000);
    client.on("custom_payload", onPayload);
  });
  client.write("custom_payload", { channel: "minecraft:register", data: Buffer.from("voicechat:secret\0voicechat:player_state\0voicechat:player_states") });
  const compatibility = Buffer.alloc(4);
  compatibility.writeInt32BE(20);
  client.write("custom_payload", { channel: "voicechat:request_secret", data: compatibility });
  const data = await secretPacket;
  const secret = data.subarray(0, 16);
  const port = data.readInt32BE(16);
  const uuid = data.subarray(20, 36);
  assert.equal(uuid.toString("hex"), bot.player.uuid.replaceAll("-", ""));
  const socket = createSocket("udp4");
  const send = (type, body = Buffer.alloc(0)) => {
    const iv = randomBytes(12);
    const cipher = createCipheriv("aes-128-gcm", secret, iv);
    const ciphertext = Buffer.concat([cipher.update(Buffer.concat([Buffer.from([type]), body])), cipher.final()]);
    const payload = Buffer.concat([iv, ciphertext, cipher.getAuthTag()]);
    socket.send(Buffer.concat([Buffer.from([255]), uuid, varint(payload.length), payload]), port, "127.0.0.1");
  };
  let authenticated = false;
  let validated = false;
  const connected = new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("Voice UDP validation timed out")), 15_000);
    socket.on("error", reject);
    socket.on("message", (packet) => {
      try {
        assert.equal(packet[0], 255);
        let offset = 1;
        while (packet[offset++] & 128) { /* skip byte-array length */ }
        const payload = packet.subarray(offset);
        const decipher = createDecipheriv("aes-128-gcm", secret, payload.subarray(0, 12));
        decipher.setAuthTag(payload.subarray(-16));
        const decoded = Buffer.concat([decipher.update(payload.subarray(12, -16)), decipher.final()]);
        if (decoded[0] === 6) { authenticated = true; send(9); }
        if (decoded[0] === 10) { validated = true; clearTimeout(timer); resolve(); }
        if (decoded[0] === 8) send(8);
      } catch (error) { clearTimeout(timer); reject(error); }
    });
  });
  const retry = setInterval(() => { if (!authenticated) send(5, Buffer.concat([uuid, secret])); else if (!validated) send(9); }, 500);
  send(5, Buffer.concat([uuid, secret]));
  try { await connected; }
  catch (error) { socket.close(); throw error; }
  finally { clearInterval(retry); }
  return {
    // 2.6.x update_state contains the disabled sound flag (not microphone mute).
    setDisabled(disabled) { client.write("custom_payload", { channel: "voicechat:update_state", data: Buffer.from([disabled ? 1 : 0]) }); },
    close() { socket.close(); },
  };
}
