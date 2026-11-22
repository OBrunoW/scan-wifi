export function connectWS({ onOpen, onClose, onMessage }) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws`);
  ws.onopen = () => {
    onOpen();
  };
  ws.onclose = () => {
    onClose();
    setTimeout(() => connectWS({ onOpen, onClose, onMessage }), 1500);
  };
  ws.onerror = () => ws.close();
  ws.onmessage = (ev) => {
    try {
      onMessage(JSON.parse(ev.data));
    } catch (err) {
      console.error('frame invalido', err);
    }
  };
}
