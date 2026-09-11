import os

import psycopg
from fastapi import FastAPI

app = FastAPI()


@app.get("/health")
def health():
    return {"status": "ok"}


@app.get("/database")
def database():
    with psycopg.connect(os.environ["DATABASE_URL"]) as connection:
        return {"value": connection.execute("SELECT 1").fetchone()[0]}
