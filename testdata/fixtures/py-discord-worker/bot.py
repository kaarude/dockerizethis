import asyncio
import os

import discord

client = discord.Client(intents=discord.Intents.default())


async def idle():
    print("worker started in offline mode", flush=True)
    while True:
        await asyncio.sleep(1)


if __name__ == "__main__":
    token = os.getenv("DISCORD_TOKEN")
    if token:
        client.run(token)
    else:
        asyncio.run(idle())
