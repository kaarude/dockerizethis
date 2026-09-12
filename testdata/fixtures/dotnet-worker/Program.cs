var token = Environment.GetEnvironmentVariable("DISCORD_TOKEN")
    ?? throw new InvalidOperationException("DISCORD_TOKEN is required");
Console.WriteLine($"bot starting ({token.Length} token chars)");
while (true)
{
    Console.WriteLine("tick");
    Thread.Sleep(5000);
}
