export function viewAlertChart(symbol:string):void {window.dispatchEvent(new CustomEvent("etape:alert-chart",{detail:symbol}));}
