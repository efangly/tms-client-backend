using TmsBackend.Models;

namespace TmsBackend.Tests.Models;

// Ported from internal/models/models_test.go.
public class MasterMachineTests
{
    [Fact]
    public void Min_max_adj_return_value_or_go_defaults()
    {
        Assert.Equal(15.5, new MasterMachine { MinTemp = 15.5 }.GetMinTemp());
        Assert.Equal(0, new MasterMachine().GetMinTemp());
        Assert.Equal(35.0, new MasterMachine { MaxTemp = 35.0 }.GetMaxTemp());
        Assert.Equal(100, new MasterMachine().GetMaxTemp());
        Assert.Equal(-1.5, new MasterMachine { AdjTemp = -1.5 }.GetAdjTemp());
        Assert.Equal(0, new MasterMachine().GetAdjTemp());
    }

    [Theory]
    [InlineData("t", true, false, false, "Temperature", "°C")]
    [InlineData("", true, false, false, "Temperature", "°C")]
    [InlineData("T", true, false, false, "Temperature", "°C")]
    [InlineData("h", false, true, false, "Humidity", "%")]
    [InlineData("p", false, false, true, "Power", "W")]
    public void Sensor_type_helpers(string sType, bool isTemp, bool isHumidity, bool isPower, string label, string unit)
    {
        var m = new MasterMachine { SType = sType };
        Assert.Equal(isTemp, m.IsTemperatureType());
        Assert.Equal(isHumidity, m.IsHumidityType());
        Assert.Equal(isPower, m.IsPowerType());
        Assert.Equal(label, m.GetTypeLabel());
        Assert.Equal(unit, m.GetUnit());
    }

    [Fact]
    public void Schedule_times_parse_color_field()
    {
        var m = new MasterMachine { Color = "0800, 1200,000000,bad,1630" };
        Assert.Equal(["0800", "1200", "1630"], m.GetScheduleTimes());
        Assert.True(m.HasScheduleTime("1200"));
        Assert.False(m.HasScheduleTime("0900"));
        Assert.Empty(new MasterMachine().GetScheduleTimes());
    }

    [Fact]
    public void Null_strings_read_as_empty_like_go()
    {
        var m = new MasterMachine { MachineName = null!, SType = null! };
        Assert.Equal("", m.MachineName);
        Assert.Equal("", m.SType);
    }

    [Fact]
    public void MachineWithStatus_copies_all_fields()
    {
        var m = new MasterMachine { MachineIP = "10.0.0.1", ProbeNo = 2, MachineName = "Fridge", MinTemp = 2, MaxTemp = 8, SType = "t", ChkReport = "1" };
        var w = MachineWithStatus.From(m);
        Assert.Equal("10.0.0.1", w.MachineIP);
        Assert.Equal(2, w.ProbeNo);
        Assert.Equal("Fridge", w.MachineName);
        Assert.Equal(8, w.MaxTemp);
        Assert.Equal("1", w.ChkReport);
    }
}
