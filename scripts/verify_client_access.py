"""Compatibility entry point: client access is now tested with both identity domains.
Run the real-service/browser/MCP suite without touching the user's workspace.
The current report is .artifacts/two-chains-verification.json.
"""
from verify_two_chains import main

if __name__ == '__main__':
    main()
