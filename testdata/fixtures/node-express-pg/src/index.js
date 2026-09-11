const express = require("express");
const { Pool } = require("pg");

const app = express();
const pool = new Pool({ connectionString: process.env.DATABASE_URL });
app.get("/health", async (_request, response) => {
  await pool.query("SELECT 1");
  response.json({ status: "ok" });
});
app.listen(Number(process.env.PORT) || 8080);
