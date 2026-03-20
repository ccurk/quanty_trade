import json
import sys
from production_selector_trend_ma import ProductionSelectorTrendMAStrategy


class Template13SelectorStrategy(ProductionSelectorTrendMAStrategy):
    def _required_contract_for_template_validation(self):
        self.buy(0, 0)
        self.send_order("buy", 0, 0)
        self.sell(0, 0)
        self.send_order("sell", 0, 0)
        self.close_position(0)


if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    config = json.loads(config_str)
    strategy = Template13SelectorStrategy(config)
    strategy.run()
