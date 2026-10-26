using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Services;

namespace TmsBackend.Infrastructure;

/// <summary>
/// `tms-backend.exe archive-table &lt;table&gt;`: replaces the Go script/main.go one-off
/// tool. Archives a backup table that shares temp_log's schema (e.g. one left over
/// from a rename) into the regular temp_log JSON-file + archive_manifest series,
/// deleting each day's rows once its file is verified. The table name may also
/// come from BACKUP_TABLE_NAME, as with the Go script.
/// </summary>
public static class ArchiveTableCommand
{
    public static async Task<int> RunAsync(string[] args, AppOptions options)
    {
        var table = args.Length > 1 ? args[1] : Environment.GetEnvironmentVariable("BACKUP_TABLE_NAME") ?? "";
        if (table == "")
        {
            Console.Error.WriteLine("usage: tms-backend archive-table <table>  (or set BACKUP_TABLE_NAME)");
            return 2;
        }

        using var loggerFactory = LoggerFactory.Create(b => b.AddSimpleConsole(o => o.SingleLine = true));
        var logger = loggerFactory.CreateLogger("archive-table");
        await using var db = new Db(options);
        var service = new ArchiveService(new ArchiveRepository(db), options, new AppReadiness(), TimeProvider.System,
            loggerFactory.CreateLogger<ArchiveService>());
        try
        {
            var result = await service.ArchiveFromTableAsync(table, deleteAfter: true);
            logger.LogInformation("Archived {Days} day(s), {Rows} row(s) from \"{Table}\" into the temp_log archive series",
                result.DaysArchived, result.RowsArchived, table);
            return 0;
        }
        catch (Exception e)
        {
            logger.LogError("archive failed: {Error}", e.Message);
            return 1;
        }
    }
}
