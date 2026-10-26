using TmsBackend.Config;

namespace TmsBackend.Tests.Config;

public class GoDurationTests
{
    [Theory]
    [InlineData("5m", 300_000)]
    [InlineData("5s", 5_000)]
    [InlineData("1h30m", 5_400_000)]
    [InlineData("500ms", 500)]
    [InlineData("1.5s", 1_500)]
    [InlineData(" 2m ", 120_000)]
    public void Parses_go_durations(string input, double expectedMs)
    {
        Assert.True(GoDuration.TryParse(input, out var ts));
        Assert.Equal(expectedMs, ts.TotalMilliseconds);
    }

    [Theory]
    [InlineData("")]
    [InlineData("5")]
    [InlineData("5x")]
    [InlineData("m5")]
    [InlineData("5m junk")]
    [InlineData(null)]
    public void Rejects_invalid(string? input)
    {
        Assert.False(GoDuration.TryParse(input, out _));
    }

    [Theory]
    [InlineData("5m")]
    [InlineData("5s")]
    [InlineData("1h30m")]
    [InlineData("1.5s")]
    [InlineData("500ms")]
    [InlineData("25h")]
    public void Format_round_trips(string input)
    {
        Assert.True(GoDuration.TryParse(input, out var ts));
        Assert.Equal(input, GoDuration.Format(ts));
    }
}
