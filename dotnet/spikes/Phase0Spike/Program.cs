// Phase 0 spike: verifies tis620 Thai text, datetime precision and timezone
// handling with MySqlConnector against the real DB. Read-only on real tables;
// write tests use a session TEMPORARY table that disappears on disconnect.
using System.Text;
using Dapper;
using MySqlConnector;

if (args.Length > 0 && args[0] == "serial")
{
    SerialSpike.Run(args[1], args.Length > 2 ? int.Parse(args[2]) : 9600, args.Length > 3 ? int.Parse(args[3]) : 2000);
    return;
}

Encoding.RegisterProvider(CodePagesEncodingProvider.Instance);
var colsMode = args.Length > 0 && args[0] == "cols";
if (colsMode)
    args = args[1..];
DotNetEnv.Env.Load(args.Length > 0 ? args[0] : "../../../.env");

string Env(string k, string d = "") => Environment.GetEnvironmentVariable(k) is { Length: > 0 } v ? v : d;

var csb = new MySqlConnectionStringBuilder
{
    Server = Env("DB_HOST"),
    Port = uint.Parse(Env("DB_PORT", "3306")),
    UserID = Env("DB_USER"),
    Password = Env("DB_PASSWORD"),
    Database = Env("DB_NAME"),
    CharacterSet = Env("DB_CHARSET", "tis620"),
    AllowZeroDateTime = true,
    ConvertZeroDateTime = true,
};

if (colsMode)
{
    await ColsSpike.Run(csb.ConnectionString);
    return;
}

Console.WriteLine("== Server / column info ==");
await using (var c = new MySqlConnection(csb.ConnectionString))
{
    await c.OpenAsync();
    Console.WriteLine($"server version: {c.ServerVersion}");
    foreach (var r in await c.QueryAsync("SHOW VARIABLES WHERE Variable_name IN ('character_set_client','character_set_connection','character_set_results','character_set_database','time_zone','system_time_zone')"))
        Console.WriteLine($"  {r.Variable_name} = {r.Value}");
    var cols = await c.QueryAsync(@"SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, CHARACTER_SET_NAME, DATETIME_PRECISION
        FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE()
        AND TABLE_NAME IN ('master_machine','temp_log','temp_error','archive_manifest','temp_log_archive')
        AND (DATA_TYPE IN ('datetime','timestamp','varchar','char','text') )
        ORDER BY TABLE_NAME, ORDINAL_POSITION");
    foreach (var r in cols)
        Console.WriteLine($"  {r.TABLE_NAME}.{r.COLUMN_NAME} {r.COLUMN_TYPE} cs={r.CHARACTER_SET_NAME} prec={r.DATETIME_PRECISION}");
}

Console.WriteLine("\n== tis620 read: master_machine.machine_name (raw hex vs decoded) ==");
await using (var c = new MySqlConnection(csb.ConnectionString))
{
    await c.OpenAsync();
    var rows = await c.QueryAsync<(string ip, int probe, string? name, string hex)>(
        "SELECT machine_ip, probe_no, machine_name, HEX(machine_name) FROM master_machine WHERE machine_name REGEXP '[^ -~]' LIMIT 5");
    var any = false;
    foreach (var (ip, probe, name, hex) in rows)
    {
        any = true;
        var raw = Convert.FromHexString(hex);
        Console.WriteLine($"  {ip}/{probe}: decoded='{name}' hex={hex}");
        Console.WriteLine($"     as tis620(874)='{Encoding.GetEncoding(874).GetString(raw)}'  as utf8='{Encoding.UTF8.GetString(raw)}'");
    }
    if (!any) Console.WriteLine("  (no non-ASCII machine_name rows found)");
}

Console.WriteLine("\n== tis620 write round-trip (TEMPORARY table) ==");
await using (var c = new MySqlConnection(csb.ConnectionString))
{
    await c.OpenAsync();
    await c.ExecuteAsync("CREATE TEMPORARY TABLE spike_t (name VARCHAR(50) CHARACTER SET tis620, t DATETIME(3), t0 DATETIME)");
    const string thai = "ตู้เย็นห้องยา 1";
    var now = DateTime.Now;
    await c.ExecuteAsync("INSERT INTO spike_t VALUES (@n, @t, @t)", new { n = thai, t = now });
    await c.ExecuteAsync("INSERT INTO spike_t VALUES (@n, @s, @s)", new { n = thai, s = now.ToString("yyyy-MM-dd HH:mm:ss.fff") });
    foreach (var r in await c.QueryAsync<(string name, string hex, DateTime t, DateTime t0)>("SELECT name, HEX(name), t, t0 FROM spike_t"))
        Console.WriteLine($"  name='{r.name}' ok={r.name == thai} hex={r.hex} t={r.t:yyyy-MM-dd HH:mm:ss.fff} t0={r.t0:yyyy-MM-dd HH:mm:ss.fff} (sent {now:HH:mm:ss.fff})");
}

Console.WriteLine("\n== datetime: latest temp_log / temp_error rows ==");
await using (var c = new MySqlConnection(csb.ConnectionString))
{
    await c.OpenAsync();
    foreach (var r in await c.QueryAsync("SELECT machine_ip, probe_no, insert_time, CAST(insert_time AS CHAR) AS raw FROM temp_log ORDER BY insert_time DESC LIMIT 3"))
        Console.WriteLine($"  temp_log {r.machine_ip}/{r.probe_no} insert_time={(DateTime)r.insert_time:yyyy-MM-dd HH:mm:ss.fff} kind={((DateTime)r.insert_time).Kind} raw='{r.raw}'");
    foreach (var r in await c.QueryAsync("SELECT machine_ip, probe_no, error_time, CAST(error_time AS CHAR) AS raw FROM temp_error ORDER BY error_time DESC LIMIT 3"))
        Console.WriteLine($"  temp_error {r.machine_ip}/{r.probe_no} error_time={(DateTime)r.error_time:yyyy-MM-dd HH:mm:ss.fff} raw='{r.raw}'");
}

Console.WriteLine("\n== timezone ==");
var tz = TimeZoneInfo.FindSystemTimeZoneById("SE Asia Standard Time");
Console.WriteLine($"  SE Asia Standard Time offset={tz.BaseUtcOffset}  thaiNow={TimeZoneInfo.ConvertTime(DateTimeOffset.UtcNow, tz):yyyy-MM-dd HH:mm:ss.fff zzz}");
var tz2 = TimeZoneInfo.FindSystemTimeZoneById("Asia/Bangkok");
Console.WriteLine($"  Asia/Bangkok (ICU) resolves: {tz2.Id} offset={tz2.BaseUtcOffset}");
Console.WriteLine($"  local machine tz={TimeZoneInfo.Local.Id} offset={TimeZoneInfo.Local.BaseUtcOffset}");
