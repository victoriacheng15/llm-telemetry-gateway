import time
import random
import json
import urllib.request
import argparse
import sys

def generate_prompt():
    if random.choice([True, False]):
        # Sensitive PII prompt
        templates = [
            "My Social Security Number is {SSN}.",
            "Please charge the transaction to card {CC}.",
            "My Canadian Social Insurance Number is {SIN}.",
            "SSN of client: {SSN}",
            "Visa Card: {CC}",
            "Client SIN details: {SIN}"
        ]
        template = random.choice(templates)
        ssn = f"{random.randint(100, 999)}-{random.randint(10, 99)}-{random.randint(1000, 9999)}"
        cc = f"{random.randint(1000, 9999)}-{random.randint(1000, 9999)}-{random.randint(1000, 9999)}-{random.randint(1000, 9999)}"
        sin = f"{random.randint(100, 999)}-{random.randint(10, 999)}-{random.randint(100, 999)}"
        return template.format(SSN=ssn, CC=cc, SIN=sin)
    else:
        # Non-sensitive nominal prompt
        templates = [
            "What is the capital of France?",
            "Write a hello world program in Go.",
            "Explain the difference between a process and a thread.",
            "Can you write a poem about artificial intelligence?",
            "What is 2 + 2?",
            "Recommend three books on system architecture.",
            "Explain Docker containers like I am five.",
            "How do I clean up dead Docker containers?"
        ]
        return random.choice(templates)

def send_traffic(url, model):
    prompt = generate_prompt()
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}]
    }
    
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST"
    )
    
    print(f"[{time.strftime('%H:%M:%S')}] Outgoing Prompt: {prompt}")
    start_time = time.time()
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            res_data = response.read().decode("utf-8")
            elapsed = (time.time() - start_time) * 1000
            print(f"[{time.strftime('%H:%M:%S')}] Gateway Response (status {response.status}, took {elapsed:.1f}ms): {res_data}\n")
    except Exception as e:
        elapsed = (time.time() - start_time) * 1000
        print(f"[{time.strftime('%H:%M:%S')}] Error sending request (took {elapsed:.1f}ms): {e}\n")

def main():
    parser = argparse.ArgumentParser(description="Synthetic traffic generator for completions proxy.")
    parser.add_argument("--url", default="http://localhost:8080/v1/chat/completions", help="Endpoint of completions gateway.")
    parser.add_argument("--model", default="qwen2.5:0.5b", help="Model targeting completions gateway.")
    parser.add_argument("--interval", type=float, default=2.0, help="Interval in seconds between requests.")
    parser.add_argument("--count", type=int, default=0, help="Number of requests to send (0 runs indefinitely).")
    
    args = parser.parse_args()
    
    print(f"Starting traffic generator.")
    print(f"- Targeting completions endpoint: {args.url}")
    print(f"- Target model: {args.model}")
    print(f"- Delay interval: {args.interval}s")
    if args.count > 0:
        print(f"- Limit: {args.count} requests")
    else:
        print("- Limit: Running indefinitely (Ctrl+C to terminate)")
    print("=" * 60 + "\n")
    
    sent = 0
    try:
        while True:
            send_traffic(args.url, args.model)
            sent += 1
            if args.count > 0 and sent >= args.count:
                print(f"Sent {sent} requests. Terminating.")
                break
            time.sleep(args.interval)
    except KeyboardInterrupt:
        print("\nTraffic generator terminated by user.")
        sys.exit(0)

if __name__ == "__main__":
    main()
