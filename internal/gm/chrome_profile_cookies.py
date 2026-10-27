"""Read only the explicitly selected Chrome cookie store; never use profile discovery."""
import json
from pathlib import Path
import sys

import browser_cookie3

cookie_file = Path(sys.argv[1])
if not cookie_file.is_file():
    raise FileNotFoundError("The configured Chrome cookie file is unavailable")
cookies = browser_cookie3.chrome(cookie_file=str(cookie_file), domain_name="google.com")
print(json.dumps({
    cookie.name: cookie.value
    for cookie in cookies
    if cookie.domain in {".google.com", "google.com", "messages.google.com", ".messages.google.com"}
    and not cookie.is_expired()
}))
