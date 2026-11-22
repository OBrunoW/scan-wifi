import argparse
import json
import socket
import sys
import time
import urllib.error
import urllib.request

SUBCARRIERS = 64
EXIT_NO_PEER = 2
EXIT_HTTP = 3


def build_line(node_id: str, sequence: int) -> str:
    fields = ["CSI", node_id, str(int(time.time() * 1000)), str(sequence), str(SUBCARRIERS)]
    for index in range(SUBCARRIERS):
        fields.append(str(10 + index % 7))
        fields.append(str(100 * (index % 5)))
    return ",".join(fields)


def send_lines(host: str, port: int, node_id: str, count: int) -> None:
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        for sequence in range(count):
            sock.sendto(build_line(node_id, sequence).encode(), (host, port))
            time.sleep(0.02)


def fetch_snapshot(host: str, http_port: int) -> dict:
    url = f"http://{host}:{http_port}/api/udp"
    with urllib.request.urlopen(url, timeout=3) as response:
        return json.load(response)


def find_peer(snapshot: dict, node_id: str) -> dict | None:
    for peer in snapshot.get("peers", []):
        if peer.get("hint") == node_id:
            return peer
    return None


def main() -> int:
    parser = argparse.ArgumentParser(description="Send a simulated CSI datagram and confirm it in /api/udp")
    parser.add_argument("host")
    parser.add_argument("udp_port", type=int)
    parser.add_argument("--http-port", type=int, default=8787)
    parser.add_argument("--node-id", default="ESP_SMOKE")
    parser.add_argument("--count", type=int, default=5)
    args = parser.parse_args()

    send_lines(args.host, args.udp_port, args.node_id, args.count)
    time.sleep(0.5)
    try:
        snapshot = fetch_snapshot(args.host, args.http_port)
    except (urllib.error.URLError, OSError, json.JSONDecodeError) as error:
        print(f"snapshot request failed: {error}", file=sys.stderr)
        return EXIT_HTTP
    peer = find_peer(snapshot, args.node_id)
    if peer is None:
        print(f"node {args.node_id} not found in snapshot: {json.dumps(snapshot)}", file=sys.stderr)
        return EXIT_NO_PEER
    print(f"ok: node {args.node_id} seen as slot {peer.get('slot')} with {peer.get('pkts')} packets")
    return 0


if __name__ == "__main__":
    sys.exit(main())
